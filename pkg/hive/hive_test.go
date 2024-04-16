package hive

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_wrapper(t *testing.T) {
	h := New()
	require.NotNil(t, h)

	err := h.Start(context.TODO())
	require.NoError(t, err)

	err = h.Stop(context.TODO())
	require.NoError(t, err)
}
