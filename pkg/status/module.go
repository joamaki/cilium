// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package status

import (
	"context"
	"time"

	"go.uber.org/fx"

	"github.com/cilium/cilium/api/v1/models"
	"github.com/cilium/cilium/pkg/defaults"
)

// Config is the collector configuration
type Config struct {
	WarningThreshold time.Duration
	FailureThreshold time.Duration
	Interval         time.Duration
}

var DefaultConfig = Config{
	WarningThreshold: defaults.StatusCollectorWarningThreshold,
	FailureThreshold: defaults.StatusCollectorFailureThreshold,
	Interval:         defaults.StatusCollectorInterval,
}

// Status is passed to a probe when its state changes
type Status struct {
	// Data is non-nil when the probe has completed successfully. Data is
	// set to the value returned by Probe()
	Data interface{}

	// Err is non-nil if either the probe file or the Failure or Warning
	// threshold has been reached
	Err error

	// StaleWarning is true once the WarningThreshold has been reached
	StaleWarning bool
}

// Probe is run by the collector at a particular interval between invocations
type Probe struct {
	Name string

	Probe func(ctx context.Context) (interface{}, error)

	// OnStatusUpdate is called whenever the status of the probe changes to update
	// the status response.
	OnStatusUpdate func(status Status, statusResponse *models.StatusResponse)

	// Interval allows to specify a probe specific interval that can be
	// mutated based on whether the probe is failing or based on external
	// factors such as current cluster size
	Interval func(failures int) time.Duration

	// consecutiveFailures is the number of consecutive failures in the
	// probe becoming stale or failing. It is managed by
	// updateProbeStatus()
	consecutiveFailures int
}

type CollectorParams struct {
	fx.In
	fx.Lifecycle

	// Config is the collector configuration
	Config

	// Probes is the initial set of probes provided by other modules at construction
	// time. More probes can be registerd with 'RegisterProbes' before start.
	Probes []Probe `group:"probes"`
}

// Collector concurrently runs probes used to check status of various subsystems
type Collector interface {
	// GetStaleProbes returns a map of stale probes which key is a probe name and
	// value is a time when the last instance of the probe has been started.
	//
	// A probe is declared stale if it hasn't returned in FailureThreshold.
	GetStaleProbes() map[string]time.Time

	// RegisterProbes allows registering more probes. Must be called before application
	// start to have an effect.
	RegisterProbes(probes ...Probe)

	// UpdateStatusResponse updates the status response. Used to update the status of
	// subsystems that have static state and no probe.
	UpdateStatusResponse(update func(*models.StatusResponse))

	// GetStatusResponse returns a deep copy of the status response.
	GetStatusResponse() *models.StatusResponse
}

var Module = fx.Module(
	"status",
	fx.Provide(newCollector),
)
