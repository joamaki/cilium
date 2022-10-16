// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package resource

import (
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
)

type Option func(*options)

// WithRateLimiter sets the rate limiter to use with the resource.
func WithRateLimiter(newLimiter func() workqueue.RateLimiter) Option {
	return func(o *options) { o.rateLimiter = newLimiter }
}

// WithErrorHandler sets the function that decides how to handle
// an error from event processing.
func WithErrorHandler(h ErrorHandler) Option {
	return func(o *options) { o.errorHandler = h }
}

// WithTransform sets a function to transform the objects before
// they're added to the store and emitted.
func WithTransform(transform cache.TransformFunc) Option {
	return func(o *options) { o.transform = transform }
}

type options struct {
	rateLimiter  func() workqueue.RateLimiter
	errorHandler ErrorHandler
	transform    cache.TransformFunc
}

func defaultOptions() options {
	return options{
		rateLimiter: func() workqueue.RateLimiter {
			return workqueue.DefaultControllerRateLimiter()
		},
		errorHandler: AlwaysRetry,
		transform:    nil,
	}
}
