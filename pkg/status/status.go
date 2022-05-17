// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package status

import (
	"context"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"
	"go.uber.org/fx"

	"github.com/cilium/cilium/api/v1/models"
	"github.com/cilium/cilium/pkg/inctimer"
	"github.com/cilium/cilium/pkg/lock"
	"github.com/cilium/cilium/pkg/logging"
	"github.com/cilium/cilium/pkg/logging/logfields"
)

const (
	subsystem = "status"
)

var (
	log = logging.DefaultLogger.WithField(logfields.LogSubsys, subsystem)
)

// Collector concurrently runs probes used to check status of various subsystems
type collector struct {
	lock.RWMutex
	config         Config
	stopChan       chan struct{}
	probes         []Probe
	staleProbes    map[string]struct{}
	probeStartTime map[string]time.Time

	statusResponse models.StatusResponse
}

// NewCollector creates a collector and starts the given probes.
//
// Each probe runs in a separate goroutine.
func newCollector(p CollectorParams) Collector {
	c := &collector{
		config:         p.Config,
		stopChan:       make(chan struct{}),
		probes:         p.Probes,
		staleProbes:    make(map[string]struct{}),
		probeStartTime: make(map[string]time.Time),
	}

	p.Lifecycle.Append(fx.Hook{OnStart: c.start, OnStop: c.stop})
	return c
}

func (c *collector) start(context.Context) error {
	for i := range c.probes {
		c.spawnProbe(&c.probes[i])
	}
	return nil
}

func (c *collector) stop(context.Context) error {
	close(c.stopChan)
	return nil
}

func (c *collector) UpdateStatusResponse(update func(*models.StatusResponse)) {
	c.Lock()
	defer c.Unlock()
	update(&c.statusResponse)
}

func (c *collector) GetStatusResponse() *models.StatusResponse {
	c.RLock()
	defer c.RUnlock()
	return c.statusResponse.DeepCopy()
}

// RegisterProbes allows registering more probes. Must be called before application
// start to have an effect.
func (c *collector) RegisterProbes(probes ...Probe) {
	c.Lock()
	defer c.Unlock()
	c.probes = append(c.probes, probes...)
}

// GetStaleProbes returns a map of stale probes which key is a probe name and
// value is a time when the last instance of the probe has been started.
//
// A probe is declared stale if it hasn't returned in FailureThreshold.
func (c *collector) GetStaleProbes() map[string]time.Time {
	c.RLock()
	defer c.RUnlock()

	probes := make(map[string]time.Time, len(c.staleProbes))

	for p := range c.staleProbes {
		probes[p] = c.probeStartTime[p]
	}

	return probes
}

// spawnProbe starts a goroutine which invokes the probe at the particular interval.
func (c *collector) spawnProbe(p *Probe) {
	go func() {
		timer, stopTimer := inctimer.New()
		defer stopTimer()
		for {
			c.runProbe(p)

			interval := c.config.Interval
			if p.Interval != nil {
				interval = p.Interval(p.consecutiveFailures)
			}
			select {
			case <-c.stopChan:
				// collector is closed, stop looping
				return
			case <-timer.After(interval):
				// keep looping
			}
		}
	}()
}

// runProbe runs the given probe, and returns either after the probe has returned
// or after the collector has been closed.
func (c *collector) runProbe(p *Probe) {
	var (
		statusData       interface{}
		err              error
		warningThreshold = time.After(c.config.WarningThreshold)
		hardTimeout      = false
		probeReturned    = make(chan struct{}, 1)
		ctx, cancel      = context.WithTimeout(context.Background(), c.config.FailureThreshold)
		ctxTimeout       = make(chan struct{}, 1)
	)

	c.Lock()
	c.probeStartTime[p.Name] = time.Now()
	c.Unlock()

	go func() {
		statusData, err = p.Probe(ctx)
		close(probeReturned)
	}()

	go func() {
		// Once ctx.Done() has been closed, we notify the polling loop by
		// sending to the ctxTimeout channel. We cannot just close the channel,
		// because otherwise the loop will always enter the "<-ctxTimeout" case.
		<-ctx.Done()
		ctxTimeout <- struct{}{}
	}()

	// This is a loop so that, when we hit a FailureThreshold, we still do
	// not return until the probe returns. This is to ensure the same probe
	// does not run again while it is blocked.
	for {
		select {
		case <-c.stopChan:
			// Collector was closed. The probe will complete in the background
			// and won't be restarted again.
			cancel()
			return

		case <-warningThreshold:
			// Just warn and continue waiting for probe
			log.WithField(logfields.Probe, p.Name).
				Warnf("No response from probe within %v seconds",
					c.config.WarningThreshold.Seconds())

		case <-probeReturned:
			// The probe completed and we can return from runProbe
			switch {
			case hardTimeout:
				// FailureThreshold was already reached. Keep the failure error
				// message
			case err != nil:
				c.updateProbeStatus(p, nil, false, err)
			default:
				c.updateProbeStatus(p, statusData, false, nil)
			}

			cancel()
			return

		case <-ctxTimeout:
			// We have timed out. Report a status and mark that we timed out so we
			// do not emit status later.
			staleErr := fmt.Errorf("no response from %s probe within %v seconds",
				p.Name, c.config.FailureThreshold.Seconds())
			c.updateProbeStatus(p, nil, true, staleErr)
			hardTimeout = true
		}
	}
}

func (c *collector) updateProbeStatus(p *Probe, data interface{}, stale bool, err error) {
	// Update stale status of the probe
	c.Lock()
	defer c.Unlock()

	startTime := c.probeStartTime[p.Name]
	if stale {
		c.staleProbes[p.Name] = struct{}{}
		p.consecutiveFailures++
	} else {
		delete(c.staleProbes, p.Name)
		if err == nil {
			p.consecutiveFailures = 0
		} else {
			p.consecutiveFailures++
		}
	}

	if stale {
		log.WithFields(logrus.Fields{
			logfields.StartTime: startTime,
			logfields.Probe:     p.Name,
		}).Warn("Timeout while waiting probe")
	}

	// Notify the probe about status update
	p.OnStatusUpdate(Status{Err: err, Data: data, StaleWarning: stale}, &c.statusResponse)
}
