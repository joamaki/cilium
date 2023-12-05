package reconciler

import (
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRetries(t *testing.T) {
	rq := newRetries(Config{
		RetryBackoffMinDuration: time.Millisecond,
		RetryBackoffMaxDuration: time.Millisecond,
	})

	key1, key2, key3 := []byte{1}, []byte{2}, []byte{3}

	// Add keys to be retried in order. We assume here that 'time.Time' has
	// enough granularity for these to be added with rising retryAt times.
	rq.Add(key1)
	rq.Add(key2)
	rq.Add(key3)

	<-rq.Wait()
	key, retryAt, ok := rq.Top()
	if assert.True(t, ok) {
		rq.Pop()
		rq.Clear(key)
		assert.True(t, retryAt.Before(time.Now()), "expected item to be expired")
		assert.True(t, bytes.Equal(key1, key))
	}

	<-rq.Wait()
	key, retryAt, ok = rq.Top()
	if assert.True(t, ok) {
		rq.Pop()
		rq.Clear(key)
		assert.True(t, retryAt.Before(time.Now()), "expected item to be expired")
		assert.True(t, bytes.Equal(key2, key))
	}

	<-rq.Wait()
	key, retryAt, ok = rq.Top()
	if assert.True(t, ok) {
		rq.Pop()
		assert.True(t, retryAt.Before(time.Now()), "expected item to be expired")
		assert.True(t, bytes.Equal(key3, key))
	}

	// Retry 'key3'
	rq.Add(key3)

	<-rq.Wait()
	key, retryAt, ok = rq.Top()
	if assert.True(t, ok) {
		rq.Pop()
		rq.Clear(key)
		assert.True(t, retryAt.Before(time.Now()), "expected item to be expired")
		assert.True(t, bytes.Equal(key3, key))
	}

	_, _, ok = rq.Top()
	assert.False(t, ok)
}
