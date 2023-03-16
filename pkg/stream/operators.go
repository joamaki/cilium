// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package stream

import (
	"context"
	"time"

	"golang.org/x/time/rate"
)

//
// Operators transform the observable.
//

// Map applies a function onto values of an observable and emits the resulting values.
func Map[A, B any](src Observable[A], apply func(A) B) Observable[B] {
	return FuncObservable[B](
		func(ctx context.Context, next func(B), complete func(error)) {
			src.Observe(
				ctx,
				func(a A) { next(apply(a)) },
				complete)
		})
}

// Filter only emits the values for which the provided predicate returns true.
func Filter[T any](src Observable[T], pred func(T) bool) Observable[T] {
	return FuncObservable[T](
		func(ctx context.Context, next func(T), complete func(error)) {
			src.Observe(
				ctx,
				func(x T) {
					if pred(x) {
						next(x)
					}
				},
				complete)
		})
}

// Reduce takes an initial state, and a function 'reduce' that is called on each element
// along with a state and returns an observable with a single item: the state produced
// by the last call to 'reduce'.
func Reduce[Item, Result any](src Observable[Item], init Result, reduce func(Result, Item) Result) Observable[Result] {
	result := init
	return FuncObservable[Result](
		func(ctx context.Context, next func(Result), complete func(error)) {
			src.Observe(
				ctx,
				func(x Item) {
					result = reduce(result, x)
				},
				func(err error) {
					if err == nil {
						next(result)
					}
					complete(err)
				})
		})
}

// Distinct skips adjacent equal values.
func Distinct[T comparable](src Observable[T]) Observable[T] {
	var prev T
	first := true
	return Filter(src, func(item T) bool {
		if first {
			first = false
			prev = item
			return true
		}
		eq := prev == item
		prev = item
		return !eq
	})
}

// RetryFunc decides whether the processing should be retried given the error
type RetryFunc func(err error) bool

// Retry resubscribes to the observable if it completes with an error.
func Retry[T any](src Observable[T], shouldRetry RetryFunc) Observable[T] {
	return FuncObservable[T](
		func(ctx context.Context, next func(T), complete func(error)) {
			var observe func()
			observe = func() {
				src.Observe(
					ctx,
					next,
					func(err error) {
						if err != nil && shouldRetry(err) && ctx.Err() == nil {
							observe()
						} else {
							if ctx.Err() != nil {
								err = ctx.Err()
							}
							complete(err)
						}
					})
			}
			observe()
		})
}

// AlwaysRetry always asks for a retry regardless of the error.
func AlwaysRetry(err error) bool {
	return true
}

// BackoffRetry retries with an exponential backoff.
func BackoffRetry(shouldRetry RetryFunc, minBackoff, maxBackoff time.Duration) RetryFunc {
	backoff := minBackoff
	return func(err error) bool {
		time.Sleep(backoff)
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
		return shouldRetry(err)
	}

}

// LimitRetries limits the number of retries with the given retry method.
// e.g. LimitRetries(BackoffRetry(time.Millisecond, time.Second), 5)
func LimitRetries(shouldRetry RetryFunc, numRetries int) RetryFunc {
	return func(err error) bool {
		if numRetries <= 0 {
			return false
		}
		numRetries--
		return shouldRetry(err)
	}
}

// ToMulticast makes 'src' a multicast observable, e.g. each observer will observe
// the same sequence.
func ToMulticast[T any](src Observable[T], opts ...MulticastOpt) (mcast Observable[T], connect func(context.Context)) {
	mcast, next, complete := Multicast[T](opts...)
	connect = func(ctx context.Context) {
		src.Observe(ctx, next, complete)
	}
	return mcast, connect
}

// Throttle limits the rate at which items are emitted.
func Throttle[T any](src Observable[T], ratePerSecond float64, burst int) Observable[T] {
	return FuncObservable[T](
		func(ctx context.Context, next func(T), complete func(error)) {
			limiter := rate.NewLimiter(rate.Limit(ratePerSecond), burst)
			var limiterErr error
			subCtx, cancel := context.WithCancel(ctx)
			src.Observe(
				subCtx,
				func(item T) {
					limiterErr = limiter.Wait(ctx)
					if limiterErr != nil {
						cancel()
						return
					}
					next(item)
				},
				func(err error) {
					if limiterErr != nil {
						complete(limiterErr)
					} else {
						complete(err)
					}

				},
			)
		})
}

// Debounce emits an item only after the specified duration has lapsed since
// the previous item was emitted. Only the latest item is emitted.
//
// In:  a   b c  d         e |->
// Out: a        d         e |->
func Debounce[T any](src Observable[T], duration time.Duration) Observable[T] {
	return FuncObservable[T](
		func(ctx context.Context, next func(T), complete func(error)) {
			errs := make(chan error, 1)
			items := ToChannel(ctx, errs, src)
			go func() {
				defer close(errs)

				timer := time.NewTimer(duration)
				defer timer.Stop()

				timerElapsed := true // Do not delay the first item.
				var latest *T

				for {
					select {
					case err := <-errs:
						complete(err)
						return

					case item := <-items:
						if timerElapsed {
							next(item)
							timerElapsed = false
							latest = nil
							timer.Reset(duration)
						} else {
							latest = &item
						}

					case <-timer.C:
						if latest != nil {
							next(*latest)
							latest = nil
							timer.Reset(duration)
						} else {
							timerElapsed = true
						}
					}
				}
			}()
		})
}

// TODO docs
func Buffer[T any](src Observable[T], bufferSize int, waitTime time.Duration) Observable[[]T] {
	return BufferBy(
		src, bufferSize, waitTime,
		func(buf []T, item T) []T {
			return append(buf, item)
		},
		func(buf []T) []T {
			return nil
		},
	)

	/*
		return FuncObservable[[]T](
			func(ctx context.Context, next func([]T), complete func(error)) {
				subCtx, cancel := context.WithCancel(ctx)
				bufChan := make(chan T, bufferSize)
				src.Observe(
					ctx,
					func(item T) {
						bufChan <- item
					},
					func(err error) {
						cancel()
						complete(err)
					})
				go func() {
					timer := time.NewTimer(waitTime)
					defer timer.Stop()

					buf := make([]T, 0, bufferSize)
					for {
						select {
						case <-timer.C:
							if len(buf) > 0 {
								next(buf)
								buf = make([]T, 0, bufferSize)
							}
						case item := <-bufChan:
							buf = append(buf, item)
							if len(buf) >= bufferSize {
								next(buf)
								buf = make([]T, 0, bufferSize)
								if !timer.Stop() {
									<-timer.C
								}
								timer.Reset(waitTime)
							}
						case <-subCtx.Done():
							if len(buf) > 0 {
								next(buf)
							}
							return
						}
					}
				}()

			})*/

}

// TODO docs
func BufferBy[Buf any, T any](src Observable[T], bufferSize int, waitTime time.Duration, bufferItem func(Buf, T) Buf, resetBuffer func(Buf) Buf) Observable[Buf] {
	return FuncObservable[Buf](
		func(ctx context.Context, next func(Buf), complete func(error)) {
			subCtx, cancel := context.WithCancel(ctx)
			items := make(chan T, bufferSize)
			src.Observe(
				ctx,
				func(item T) {
					items <- item
				},
				func(err error) {
					cancel()
					complete(err)
				})
			go func() {
				timer := time.NewTimer(waitTime)
				defer timer.Stop()

				var (
					emptyBuf Buf
					buf      Buf
				)
				n := 0
				for {
					select {
					case <-timer.C:
						if n > 0 {
							next(buf)
							buf = emptyBuf
							n = 0
						}
					case item := <-items:
						buf = bufferItem(buf, item)
						n++
						if n >= bufferSize {
							next(buf)
							buf = emptyBuf
							n = 0
							if !timer.Stop() {
								<-timer.C
							}
							timer.Reset(waitTime)
						}
					case <-subCtx.Done():
						if n > 0 {
							next(buf)
						}
						return
					}
				}
			}()

		})

}
