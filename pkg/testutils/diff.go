// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package testutils

import (
	"os"
	"testing"

	"github.com/pmezard/go-difflib/difflib"
	"github.com/stretchr/testify/require"
)

// DiffExpectedFile reads the actual and expected test files and
// returns unified diff.
func DiffExpectedFile(t testing.TB, actualFile, expectedFile string) string {
	contentsA, err := os.ReadFile(actualFile)
	require.NoError(t, err, "ReadFile(%q)", actualFile)

	contentsB, err := os.ReadFile(expectedFile)
	if !os.IsNotExist(err) {
		require.NoError(t, err, "ReadFile(%q)", expectedFile)
	}

	diff := difflib.UnifiedDiff{
		A:        difflib.SplitLines(string(contentsA)),
		B:        difflib.SplitLines(string(contentsB)),
		FromFile: actualFile,
		ToFile:   expectedFile,
		Context:  2,
	}
	text, _ := difflib.GetUnifiedDiffString(diff)
	return text
}
