package resource

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type TestKey string



type TestValue struct {
	X int `json:"X"`
}

func TestPebbleJSON(t *testing.T) {

	ps, err := NewPebbleKeyValueStore(t.TempDir())
	assert.NoError(t, err)

	js := NewJSONAdapter[TestKey, TestValue](ps)

	err = js.Create(
		TestKey("hello"),
		TestValue{X: 10},
	)
	assert.NoError(t, err)

	iter := js.Keys()
	for {
		k, ok := iter.Next()
		if !ok {
			break
		}
		fmt.Printf("k: %v\n", k)
	}

}


func TestPebbleProtobuf(t *testing.T) {

	ps, err := NewPebbleKeyValueStore(t.TempDir())
	assert.NoError(t, err)

	bufs := NewProtobufAdapter[*metav1.ObjectMeta, *corev1.Node](ps)

	err = bufs.Create(
		&metav1.ObjectMeta{Name: "hello"},
		&corev1.Node{},
	)
	assert.NoError(t, err)

}
