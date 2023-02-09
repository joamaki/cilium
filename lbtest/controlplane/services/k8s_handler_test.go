package services_test

import (
	"context"
	"testing"
	"time"

	"github.com/cilium/cilium/lbtest/controlplane/servicemanager"
	"github.com/cilium/cilium/lbtest/datapath/loadbalancer"
	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/hive/cell"
	"github.com/cilium/cilium/pkg/k8s"
	"github.com/cilium/cilium/pkg/k8s/client"
	slim_corev1 "github.com/cilium/cilium/pkg/k8s/slim/k8s/api/core/v1"
	slim_metav1 "github.com/cilium/cilium/pkg/k8s/slim/k8s/apis/meta/v1"
	lb "github.com/cilium/cilium/pkg/loadbalancer"
	"github.com/cilium/cilium/pkg/logging"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type lbEvent struct {
	addr lb.L3n4Addr
	svc  *lb.SVC
}

type fakeLoadBalancer struct {
	events chan lbEvent
}

func (f *fakeLoadBalancer) Delete(addr lb.L3n4Addr) {
	f.events <- lbEvent{addr, nil}
}

func (*fakeLoadBalancer) GarbageCollect() {}

func (f *fakeLoadBalancer) Upsert(svc *lb.SVC) {
	f.events <- lbEvent{svc.Frontend.L3n4Addr, svc}
}

var _ loadbalancer.LoadBalancer = &fakeLoadBalancer{}

func TestK8sHandler(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	logging.SetLogLevelToDebug()

	flb := fakeLoadBalancer{
		events: make(chan lbEvent, 16),
	}

	var fcs *client.FakeClientset

	h := hive.New(
		client.FakeClientCell,
		cell.Invoke(func(f *client.FakeClientset) { fcs = f }),

		k8s.SharedResourcesCell,
		servicemanager.K8sHandlerCell,
		servicemanager.Cell,

		cell.Provide(func() loadbalancer.LoadBalancer { return &flb }),
	)

	assert.NoError(t, h.Start(ctx))

	_, err := fcs.Slim().CoreV1().Services("test").Create(
		ctx,
		&slim_corev1.Service{
			ObjectMeta: slim_metav1.ObjectMeta{
				Namespace: "test",
				Name:      "alpha",
			},
			Spec: slim_corev1.ServiceSpec{
				Ports: []slim_corev1.ServicePort{
					{
						Name:     "http",
						Protocol: slim_corev1.ProtocolTCP,
						Port:     33333,
						NodePort: 44444,
					},
				},
				Type: slim_corev1.ServiceTypeNodePort,
			},
		},
		metav1.CreateOptions{},
	)
	assert.NoError(t, err, "Services.Create")

	ev := <-flb.events
	assert.Equal(t, ev.svc.Name.String(), "svc/test/alpha")
	assert.Equal(t, ev.svc.Type, lb.SVCTypeNodePort)
	assert.Equal(t, ev.svc.Frontend.String(), "invalid IP:44444")
	assert.Equal(t, len(ev.svc.Backends), 0)

	_, err = fcs.Slim().CoreV1().Endpoints("test").Create(
		ctx,
		&slim_corev1.Endpoints{
			ObjectMeta: slim_metav1.ObjectMeta{
				Namespace: "test",
				Name:      "alpha",
			},
			Subsets: []slim_corev1.EndpointSubset{
				{
					Addresses: []slim_corev1.EndpointAddress{
						{IP: "2.3.4.5", NodeName: nil},
					},
					Ports: []slim_corev1.EndpointPort{
						{
							Name:     "http",
							Port:     80,
							Protocol: slim_corev1.ProtocolTCP,
						},
					},
				},
			},
		},
		metav1.CreateOptions{})

	assert.NoError(t, err, "Endpoints.Create")

	ev = <-flb.events
	assert.Equal(t, ev.svc.Name.String(), "svc/test/alpha")
	assert.Equal(t, ev.svc.Type, lb.SVCTypeNodePort)
	assert.Equal(t, ev.svc.Frontend.String(), "invalid IP:44444")
	assert.Equal(t, len(ev.svc.Backends), 1)

	assert.NoError(t, h.Stop(ctx))

}
