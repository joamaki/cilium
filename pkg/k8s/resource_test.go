package k8s

import (
	"context"
	"testing"

	cilium_v2 "github.com/cilium/cilium/pkg/k8s/apis/cilium.io/v2"
	cilium_fake "github.com/cilium/cilium/pkg/k8s/client/clientset/versioned/fake"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/apimachinery/pkg/api/errors"
)

func TestCiliumEndpointResource(t *testing.T) {
	cep := &cilium_v2.CiliumEndpoint{
		ObjectMeta: metav1.ObjectMeta{
			Name: "abc",
			Namespace: "foo",
		},
	}
	fakeClient := &K8sClient{fake.NewSimpleClientset(), nil}
	ciliumClient := cilium_fake.NewSimpleClientset()

	cepChan := make(chan *cilium_v2.CiliumEndpoint)

	subscribe := func(ceps K8sResource[*cilium_v2.CiliumEndpoint]) {
		ceps.ChangedKeys().Subscribe("test", func(key K8sKey) {
			n, err := ceps.Get(key)
			if err != nil {
				if !errors.IsNotFound(err) {
					t.Fatal(err)
				}
				cepChan <- nil
			} else {
				cepChan <- n
			}
		})
	}

	app := fxtest.New(
		t,
		fx.Supply(fakeClient, &K8sCiliumClient{ciliumClient}),
		SharedInformerModule,
		NewK8sResourceModule(CiliumEndpoint),
		fx.Invoke(subscribe),
	).RequireStart()

	ctx := context.Background()

	// Create
	ciliumClient.CiliumV2().CiliumEndpoints("foo").Create(ctx, cep, metav1.CreateOptions{})
	cepX := <-cepChan
	if cepX.Name != cep.Name {
		t.Fatal("name mismatch")
	}

	// Update the object 10 times.
	for i := 1; i <= 10; i++ {
		cep.Status.ID = int64(i)
		ciliumClient.CiliumV2().CiliumEndpoints("foo").Update(ctx, cep, metav1.UpdateOptions{})
	}
	// Consume the updates. Expecting to see less than 10 updates due to coalescing.
	n := 0
	for {
		cepX := <-cepChan
		n++
		t.Logf("observed ID: %d\n", cepX.Status.ID)
		if cepX.Status.ID == 10 {
			break
		}
	}
	t.Logf("observed %d updates\n", n)

	// Delete
	ciliumClient.CiliumV2().CiliumEndpoints("foo").Delete(ctx, cep.Name, metav1.DeleteOptions{})

	for {
		cepX = <-cepChan
		if cepX != nil {
			t.Logf("unexpected update after delete: %#v", cepX)
		} else {
			break
		}
	}

	app.RequireStop()
}
