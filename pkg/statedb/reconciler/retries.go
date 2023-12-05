package reconciler

import (
	"bytes"
	"container/heap"
	"time"

	"github.com/cilium/cilium/pkg/backoff"
)

func newRetries(minDuration, maxDuration time.Duration) *retries {
	const prealloc = 64
	return &retries{
		backoff: backoff.Exponential{
			Min: minDuration,
			Max: maxDuration,
		},
		queue: make([]*retryItem, 0, prealloc),
		items: make(map[string]*retryItem, prealloc),
	}
}

// retries holds the items that failed to be reconciled in
// a priority queue ordered by retry time.
type retries struct {
	backoff backoff.Exponential
	queue   retryPrioQueue
	items   map[string]*retryItem
}

type retryItem struct {
	key        []byte    // the primary key of the object that is being retried
	index      int       // item's index in the priority queue
	retryAt    time.Time // time at which to retry
	numRetries int       // number of retries attempted (for calculating backoff)
}

// Wait returns a channel that is closed when there is an item to retry.
func (rq *retries) Wait() <-chan struct{} {
	if _, retryAt, ok := rq.Top(); ok {
		now := time.Now()
		ch := make(chan struct{}, 1)
		if now.After(retryAt) {
			// Already expired.
			close(ch)
		} else {
			time.AfterFunc(retryAt.Sub(now), func() { close(ch) })
		}
		return ch
	}
	return nil
}

func (rq *retries) Top() (key []byte, retryAt time.Time, ok bool) {
	if rq.queue.Len() == 0 {
		return
	}
	item := rq.queue[0]
	return item.key, item.retryAt, true
}

func (rq *retries) Pop() {
	// Pop the object from the queue, but leave it into the map until
	// the object is cleared or re-added.
	heap.Pop(&rq.queue)
}

func (rq *retries) Add(key []byte) {
	var (
		retryObj *retryItem
		ok       bool
	)
	if retryObj, ok = rq.items[string(key)]; !ok {
		retryObj = &retryItem{
			key:        key,
			numRetries: 0,
		}
		rq.items[string(key)] = retryObj
	}
	retryObj.numRetries += 1
	retryObj.retryAt = time.Now().Add(rq.backoff.Duration(retryObj.numRetries))
	heap.Push(&rq.queue, retryObj)
}

func (rq *retries) Clear(key []byte) {
	if robj, ok := rq.items[string(key)]; ok {
		// Remove the object from the queue if it is still there.
		if robj.index >= 0 && robj.index < len(rq.queue) &&
			bytes.Equal(rq.queue[robj.index].key, key) {
			heap.Remove(&rq.queue, robj.index)
		}
		// Completely forget the object and its retry count.
		delete(rq.items, string(key))
	}
}

// retryPrioQueue is a slice-backed priority heap with the next
// expiring 'retryItem' at top. Implementation is adapted from the
// 'container/heap' PriorityQueue example.
type retryPrioQueue []*retryItem

func (pq retryPrioQueue) Len() int { return len(pq) }

func (pq retryPrioQueue) Less(i, j int) bool {
	return pq[i].retryAt.Before(pq[j].retryAt)
}

func (pq retryPrioQueue) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
	pq[i].index = i
	pq[j].index = j
}

func (pq *retryPrioQueue) Push(x any) {
	retryObj := x.(*retryItem)
	retryObj.index = len(*pq)
	*pq = append(*pq, retryObj)
}

func (pq *retryPrioQueue) Pop() any {
	old := *pq
	n := len(old)
	item := old[n-1]
	old[n-1] = nil  // avoid memory leak
	item.index = -1 // for safety
	*pq = old[0 : n-1]
	return item
}
