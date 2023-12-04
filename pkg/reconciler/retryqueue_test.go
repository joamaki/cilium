package reconciler

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRetryQueue(t *testing.T) {
	rq := newRetryQueue[int](Config{
		RetryBackoffMinDuration: time.Millisecond,
		RetryBackoffMaxDuration: time.Millisecond,
	})

	rq.Add(1, 1, false)
	rq.Add(2, 2, false)
	rq.Add(3, 3, false)

	<-rq.Wait()
	x, rev, delete, retryAt, ok := rq.Top()
	if assert.True(t, ok) {
		rq.Pop()
		rq.Clear(x)
		assert.False(t, delete)
		assert.True(t, retryAt.Before(time.Now()), "expected item to be expired")
		assert.EqualValues(t, x, 1)
		assert.EqualValues(t, rev, 1)
	}

	<-rq.Wait()
	x, rev, delete, retryAt, ok = rq.Top()
	if assert.True(t, ok) {
		rq.Pop()
		assert.False(t, delete)
		assert.True(t, retryAt.Before(time.Now()), "expected item to be expired")
		assert.EqualValues(t, x, 2)
		assert.EqualValues(t, rev, 2)
		rq.Clear(x)
	}

	<-rq.Wait()
	x, rev, delete, retryAt, ok = rq.Top()
	if assert.True(t, ok) {
		rq.Pop()
		assert.False(t, delete)
		assert.True(t, retryAt.Before(time.Now()), "expected item to be expired")
		assert.EqualValues(t, x, 3)
		assert.EqualValues(t, rev, 3)
	}

	// Retry
	rq.Add(x, rev, false)

	<-rq.Wait()
	x, rev, delete, retryAt, ok = rq.Top()
	if assert.True(t, ok) {
		rq.Pop()
		rq.Clear(x)
		assert.False(t, delete)
		assert.True(t, retryAt.Before(time.Now()), "expected item to be expired")
		assert.EqualValues(t, x, 3)
		assert.EqualValues(t, rev, 3)
	}

	_, _, _, _, ok = rq.Top()
	assert.False(t, ok)
}
