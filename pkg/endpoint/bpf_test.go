// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

//go:build integration_tests

package endpoint

import (
	"bytes"
	"io"
	"os"
	"testing"

	. "gopkg.in/check.v1"

	"github.com/cilium/cilium/pkg/datapath/fake"
	"github.com/cilium/cilium/pkg/datapath/linux"
	testidentity "github.com/cilium/cilium/pkg/testutils/identity"
	testipcache "github.com/cilium/cilium/pkg/testutils/ipcache"
)

func (s *EndpointSuite) TestWriteInformationalComments(c *C) {
	e := NewEndpointWithState(s, s, testipcache.NewMockIPCache(), &FakeEndpointProxy{}, testidentity.NewMockIdentityAllocator(nil), 100, StateWaitingForIdentity)

	var f bytes.Buffer
	err := e.writeInformationalComments(&f)
	c.Assert(err, IsNil)
}

type writeFunc func(io.Writer) error

func BenchmarkWriteHeaderfile(b *testing.B) {
	e := NewEndpointWithState(&suite, &suite, testipcache.NewMockIPCache(), &FakeEndpointProxy{}, testidentity.NewMockIdentityAllocator(nil), 100, StateWaitingForIdentity)
	nodeAddressing := fake.NewNodeAddressing()
	dp := linux.NewDatapath(linux.DatapathConfiguration{}, nil, nil, nodeAddressing)

	targetComments := func(w io.Writer) error {
		return e.writeInformationalComments(w)
	}
	targetConfig := func(w io.Writer) error {
		return dp.WriteEndpointConfig(w, e)
	}

	var buf bytes.Buffer
	file, err := os.CreateTemp("", "cilium_ep_bench_")
	if err != nil {
		b.Fatal(err)
	}
	defer file.Close()

	benchmarks := []struct {
		name   string
		output io.Writer
		write  writeFunc
	}{
		{"in-memory-info", &buf, targetComments},
		{"in-memory-cfg", &buf, targetConfig},
		{"to-disk-info", file, targetComments},
		{"to-disk-cfg", file, targetConfig},
	}

	for _, bm := range benchmarks {
		b.Run(bm.name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if err := bm.write(bm.output); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
