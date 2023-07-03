package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// 'testdata' contains two directories:
//   - 'valid' manifests that should pass without error
//   - 'invalid' pairs of manifests that should fail (*.yaml files) and their
//     expected error message (*.out files)
var testdataDir string

func init() {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		panic("Can't get current filename to find 'testdata'")
	}
	testdataDir = filepath.Join(filepath.Dir(file), "testdata")
}

func TestFailsOnUnreadableFile(t *testing.T) {
	unreadableFile := filepath.Join(t.TempDir(), "unreadable")
	f, err := os.Create(unreadableFile)
	require.NoError(t, err)
	require.NoError(t, f.Chmod(0o200))
	expectedErrPrefix := "Failed opening manifest: " + unreadableFile

	err = validateOpsLevelAnnotationsForManifests([]string{unreadableFile})

	require.ErrorContains(t, err, expectedErrPrefix)
}

func TestFailsOnUnparseableManifest(t *testing.T) {
	invalidManifest := `apiVersion: apps/v1
    kind: made-up-kind
    metadata:
      name: my-deployment
`
	manifestPath := filepath.Join(t.TempDir(), "deployment.yaml")
	require.NoError(t, os.WriteFile(manifestPath, []byte(invalidManifest), 0o600))
	expectedErrPrefix := "Failed reading manifests from " + manifestPath

	err := validateOpsLevelAnnotationsForManifests([]string{manifestPath})

	require.ErrorContains(t, err, expectedErrPrefix)
}

func TestFailsOnInvalidManifests(t *testing.T) {
	invalidManifestsDir := filepath.Join(testdataDir, "invalid")
	entries, err := os.ReadDir(invalidManifestsDir)
	require.NoError(t, err, "could not read form testdata dir, has it moved?")

	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".yaml" {
			t.Run(entry.Name(), func(t *testing.T) {
				manifest := filepath.Join(invalidManifestsDir, entry.Name())

				// read expected error from corresponding '.out' file
				expectedOutFile := strings.Replace(entry.Name(), ".yaml", ".out", 1)
				expectedOut, err := os.ReadFile(filepath.Join(invalidManifestsDir, expectedOutFile))
				require.NoErrorf(t, err, "Can't find output file for %s", entry.Name())
				// .out files contain trailing newlines, but the error will not
				expectedErr := strings.TrimSuffix(fmt.Sprintf("failed validating manifests from %s: %s", manifest, string(expectedOut)), "\n")

				gotErr := validateOpsLevelAnnotationsForManifests([]string{manifest})
				require.Error(t, gotErr)
				require.EqualError(t, gotErr, expectedErr)
			})
		}
	}
}

func TestAcceptsValidManifests(t *testing.T) {
	validManifestsDir := filepath.Join(testdataDir, "valid")
	entries, err := os.ReadDir(validManifestsDir)
	require.NoError(t, err, "could not read form testdata dir, has it moved?")

	for _, entry := range entries {
		manifest := filepath.Join(validManifestsDir, entry.Name())
		t.Run(entry.Name(), func(t *testing.T) {
			require.NoError(t, validateOpsLevelAnnotationsForManifests([]string{manifest}))
		})
	}
}
