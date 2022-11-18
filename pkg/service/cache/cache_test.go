package cache

import (
	"context"
	"fmt"
	"net"
	"testing"

	"github.com/cilium/cilium/pkg/k8s/resource"
	slim_corev1 "github.com/cilium/cilium/pkg/k8s/slim/k8s/api/core/v1"
	"github.com/cilium/cilium/pkg/loadbalancer"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"

	cmtypes "github.com/cilium/cilium/pkg/clustermesh/types"
	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/hive/cell"
	"github.com/cilium/cilium/pkg/k8s"
	slim_metav1 "github.com/cilium/cilium/pkg/k8s/slim/k8s/apis/meta/v1"
	"github.com/cilium/cilium/pkg/stream"

	fakeDatapath "github.com/cilium/cilium/pkg/datapath/fake"
)

var (
	gopherAddr = loadbalancer.NewL4Addr(loadbalancer.L4Type("TCP"), uint16(70))
)

type cacheResources struct {
	cell.Out
	Nodes     resource.Resource[*corev1.Node]
	Services  resource.Resource[*slim_corev1.Service]
	Endpoints resource.Resource[*k8s.Endpoints]
}

func TestServiceCache(t *testing.T) {
	var cache ServiceCache

	mockNodes := resource.NewMockResource[*corev1.Node]()
	mockServices := resource.NewMockResource[*slim_corev1.Service]()
	mockEndpoints := resource.NewMockResource[*k8s.Endpoints]()
	mocks := cacheResources{
		Nodes:     mockNodes,
		Services:  mockServices,
		Endpoints: mockEndpoints,
	}

	testHive := hive.New(
		// Dependencies:
		cell.Provide(func() cacheResources { return mocks }),
		cell.Provide(fakeDatapath.NewNodeAddressing),
		// ServiceCache itself:
		Cell,
		// Pull out the constructed ServiceCache so we can test
		// against it.
		cell.Invoke(func(s ServiceCache) {
			cache = s
		}),
	)

	err := testHive.Start(context.TODO())
	assert.NoError(t, err, "expected hive.Start to succeed")

	ctx, cancel := context.WithCancel(context.Background())
	errs := make(chan error)
	events := stream.ToChannel[*ServiceEvent](ctx, errs, cache, stream.BufferSize(16))

	// FIXME test node labels
	// mockNodes.EmitUpdate(...)
	mockNodes.EmitSync()

	serviceID := k8s.ServiceID{Name: "svc1", Namespace: "default"}
	serviceClusterIP := "1.2.3.4"

	// 1. Emit the service and mark services synced.

	mockServices.EmitUpdate(&slim_corev1.Service{
		ObjectMeta: slim_metav1.ObjectMeta{Namespace: serviceID.Namespace, Name: serviceID.Name, ResourceVersion: "1"},
		Spec: slim_corev1.ServiceSpec{
			Type:      slim_corev1.ServiceTypeClusterIP,
			ClusterIP: serviceClusterIP,
		},
		Status: slim_corev1.ServiceStatus{},
	})
	mockServices.EmitSync()

	epSliceID := k8s.EndpointSliceID{
		ServiceID:         serviceID,
		EndpointSliceName: serviceID.Name,
	}

	nodeName := "test-node"
	backendClusterIP := cmtypes.MustParseAddrCluster("2.3.4.5")

	// 2. Emit the an endpoint for the service and mark endpoints synced.

	mockEndpoints.EmitUpdate(&k8s.Endpoints{
		EndpointSliceID: epSliceID,
		Backends: map[cmtypes.AddrCluster]*k8s.Backend{
			backendClusterIP: {
				NodeName: nodeName,
				Ports:    map[string]*loadbalancer.L4Addr{"gopher": gopherAddr},
			},
		},
	})
	mockEndpoints.EmitSync()

	// 3. Pull the first ServiceEvent and validate.

	ev := <-events
	assert.Equal(t, UpdateService, ev.Action)
	assert.Equal(t, serviceID, ev.ID)
	assert.Nil(t, ev.OldService)
	// Test that the service is set correctly. We assume parsing of services is tested separately.
	assert.Equal(t, []net.IP{net.ParseIP(serviceClusterIP)}, ev.Service.FrontendIPs)

	fmt.Printf("got service event: %#v\n", ev)

	// 4. Unsubscribe and verify completion.
	cancel()

	// No further events are expected.
	ev, ok := <-events
	assert.Nil(t, ev)
	assert.False(t, ok)

	// Error should be canceled from the call to cancel().
	err = <-errs
	assert.ErrorIs(t, err, context.Canceled, "expected service event stream to complete with context cancelation")

	err = testHive.Stop(context.TODO())
	assert.NoError(t, err, "expected hive.Stop to succeed")

}
