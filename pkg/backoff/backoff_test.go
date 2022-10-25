// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package backoff

import (
	"fmt"
	"testing"
	"time"

	"gopkg.in/check.v1"
)

func Test(t *testing.T) {
	check.TestingT(t)
}

type BackoffSuite struct{}

var _ = check.Suite(&BackoffSuite{})

func (b *BackoffSuite) TestJitter(c *check.C) {
	var prev time.Duration
	for i := 0; i < 100; i++ {
		current := CalculateDuration(time.Second, time.Minute, 2.0, true, 1)
		c.Assert(current, check.Not(check.Equals), prev)
		prev = current
	}
}

func (b *BackoffSuite) TestClusterSizeDependantInterval(c *check.C) {
	var (
		clusterSizeBackoff = &ClusterSizeBackoff{}
	)

	nodeBackoff := &Exponential{
		Min:                time.Second,
		Max:                2 * time.Minute,
		ClusterSizeBackoff: clusterSizeBackoff,
		Jitter:             true,
		Factor:             2.0,
	}

	fmt.Printf("nodes      4        16       128       512      1024      2048\n")
	for attempt := 1; attempt <= 8; attempt++ {
		fmt.Printf("%d:", attempt)
		for _, n := range []int{4, 16, 128, 512, 1024, 2048} {
			clusterSizeBackoff.UpdateNodeCount(n)
			fmt.Printf("%10s", nodeBackoff.Duration(attempt).Round(time.Second/10))
		}
		fmt.Printf("\n")
	}
}

func (b *BackoffSuite) TestJitterDistribution(c *check.C) {
	nodeBackoff := &Exponential{
		Min:    time.Second,
		Factor: 2.0,
	}

	for attempt := 1; attempt <= 8; attempt++ {
		current := nodeBackoff.Duration(attempt).Round(time.Second / 10)
		fmt.Printf("%d: %s\n", attempt, current)
	}
}
