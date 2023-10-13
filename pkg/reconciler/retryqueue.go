package reconciler

import (
	"container/heap"
	"time"

	"github.com/cilium/cilium/pkg/backoff"
	"github.com/cilium/cilium/pkg/statedb"
)

func newRetryQueue[Obj comparable](cfg Config) *retryQueue[Obj] {
	return &retryQueue[Obj]{
		backoff: backoff.Exponential{
			Min: cfg.RetryBackoffMinDuration,
			Max: cfg.RetryBackoffMaxDuration,
		},
		queue: make([]*retryObject[Obj], 0, 32),
		objs:  make(map[Obj]*retryObject[Obj]),
	}
}

// retryQueue holds the objects that failed to be reconciled in
// a priority queue ordered by retry time.
type retryQueue[Obj comparable] struct {
	backoff backoff.Exponential
	queue   retryPrioQueue[Obj]
	objs    map[Obj]*retryObject[Obj]
}

type retryObject[Obj any] struct {
	object     Obj
	revision   statedb.Revision
	delete     bool
	index      int
	retryAt    time.Time
	numRetries int
}

func (rq *retryQueue[Obj]) Clear(obj Obj) {
	if robj, ok := rq.objs[obj]; ok {
		if rq.queue[robj.index].object == obj {
			// Remove the object from the queue.
			heap.Remove(&rq.queue, robj.index)
		}
		// Completely forget the object.
		delete(rq.objs, obj)
	}
}

func (rq *retryQueue[Obj]) Wait() <-chan time.Time {
	if _, _, _, retryAt, ok := rq.Top(); ok {
		return time.After(time.Now().Sub(retryAt))
	}
	return nil
}

func (rq *retryQueue[Obj]) Top() (obj Obj, rev statedb.Revision, delete bool, retryAt time.Time, ok bool) {
	if rq.queue.Len() == 0 {
		return
	}
	retryObj := rq.queue[0]
	return retryObj.object, retryObj.revision, retryObj.delete, retryObj.retryAt, true
}

func (rq *retryQueue[Obj]) Pop() {
	// Pop the object from the queue, but leave it into the map until
	// the object is cleared or re-added.
	heap.Pop(&rq.queue)
}

func (rq *retryQueue[Obj]) Add(obj Obj, rev statedb.Revision, delete bool) {
	var (
		retryObj *retryObject[Obj]
		ok       bool
	)
	if retryObj, ok = rq.objs[obj]; !ok {
		retryObj = &retryObject[Obj]{
			object:     obj,
			numRetries: 0,
		}
	}
	retryObj.revision = rev
	retryObj.delete = delete
	retryObj.numRetries += 1
	retryObj.retryAt = time.Now().Add(rq.backoff.Duration(retryObj.numRetries))
	rq.objs[obj] = retryObj
	heap.Push(&rq.queue, retryObj)
}

type retryPrioQueue[Obj any] []*retryObject[Obj]

func (pq retryPrioQueue[Obj]) Len() int { return len(pq) }

func (pq retryPrioQueue[Obj]) Less(i, j int) bool {
	return pq[i].retryAt.Before(pq[j].retryAt)
}

func (pq retryPrioQueue[Obj]) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
	pq[i].index = i
	pq[j].index = j
}

func (pq *retryPrioQueue[Obj]) Push(x any) {
	retryObj := x.(*retryObject[Obj])
	retryObj.index = len(*pq)
	*pq = append(*pq, retryObj)
}

func (pq *retryPrioQueue[Obj]) Pop() any {
	old := *pq
	n := len(old)
	item := old[n-1]
	old[n-1] = nil  // avoid memory leak
	item.index = -1 // for safety
	*pq = old[0 : n-1]
	return item
}
