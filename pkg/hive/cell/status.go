// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package cell

import (
	"context"
	"fmt"
	"sync"
	"time"

	"golang.org/x/exp/maps"
	"golang.org/x/exp/slices"

	"github.com/cilium/cilium/pkg/stream"
)

type Level string

const (
	LevelDown     Level = "Down"
	LevelDegraded Level = "Degraded"
	LevelOK       Level = "OK"
)

type update struct {
	ModuleID string
	Level
	Message string
}

type StatusReporter interface {
	OK(status string)
	Down(reason string) // TODO: Disabled() instead? Don't report when stopping?
	Degraded(reason string)
}

type ModuleStatus struct {
	update
	LastOK      time.Time
	LastUpdated time.Time
}

func (s *ModuleStatus) String() string {
	return fmt.Sprintf("%-30s %-9s: %s (%.2fs ago)",
		s.ModuleID, s.Level, s.Message, time.Now().Sub(s.LastUpdated).Seconds())
}

func NewStatusProvider() *StatusProvider {
	p := &StatusProvider{
		updates:  make(chan update),
		statuses: make(map[string]ModuleStatus),
	}
	p.src, p.emit, p.complete = stream.Multicast[ModuleStatus]()
	return p
}

type moduleStatusUpdate struct {
	update update
}

type StatusProvider struct {
	mu       sync.Mutex
	updates  chan update
	statuses map[string]ModuleStatus

	src      stream.Observable[ModuleStatus]
	emit     func(ModuleStatus)
	complete func(error)
}

type reporter struct {
	*StatusProvider
	moduleID string
}

// TODO: These methods should be rate limited. Might also make sense to flip this around and
// do what pkg/status does with probing as constructing the status string isn't free.
// E.g. instead of StatusReporter being available, we'd depend on "StatusRegistry" that's
// module-id scoped and we'd be able to register multiple probes.

func (r *reporter) Degraded(reason string) {
	r.process(update{ModuleID: r.moduleID, Level: LevelDegraded, Message: reason})
}

func (r *reporter) Down(reason string) {
	r.process(update{ModuleID: r.moduleID, Level: LevelDown, Message: reason})
}

func (r *reporter) OK(status string) {
	r.process(update{ModuleID: r.moduleID, Level: LevelOK, Message: status})
}

func (p *StatusProvider) ForModule(moduleID string) StatusReporter {
	p.mu.Lock()
	p.statuses[moduleID] = ModuleStatus{update: update{ModuleID: moduleID}}
	p.mu.Unlock()
	return &reporter{moduleID: moduleID, StatusProvider: p}
}

func (p *StatusProvider) All() []ModuleStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	all := maps.Values(p.statuses)
	slices.SortFunc(all, func(a, b ModuleStatus) bool {
		return a.ModuleID < b.ModuleID
	})
	return all
}

func (p *StatusProvider) Get(moduleID string) *ModuleStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	s, ok := p.statuses[moduleID]
	if ok {
		return &s
	}
	return nil
}

// TODO: Idea with Stream() is that we could have subscriber that
// propagates to metrics ("num_degraded") etc. Alternatively we just
// integrate metrics directly into this.
// TODO: Should we emit []ModuleStatus?
func (p *StatusProvider) Stream(ctx context.Context) <-chan ModuleStatus {
	return stream.ToChannel(ctx, make(chan error, 1), p.src)
}

func (p *StatusProvider) process(u update) {
	p.mu.Lock()
	defer p.mu.Unlock()

	s := p.statuses[u.ModuleID]
	s.LastUpdated = time.Now()
	if u.Level == LevelOK {
		s.LastOK = time.Now()
	}
	s.update = u
	p.statuses[u.ModuleID] = s
	p.emit(s)
}

func (p *StatusProvider) Stop() {
	p.complete(nil)
}
