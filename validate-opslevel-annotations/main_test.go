package main

import (
	"encoding/base64"
	"fmt"
	"io"
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

func TestFailsOnNonBase64Path(t *testing.T) {
	manifestPath := filepath.Join(t.TempDir(), "not-base-64.yaml")
	_, err := os.Create(manifestPath)
	require.NoError(t, err)
	expectedErrPrefix := "expected a base64 encoded filename at " + manifestPath + ", but failed to decode: "

	err = validateOpsLevelAnnotationsForManifests([]string{manifestPath})

	require.ErrorContains(t, err, expectedErrPrefix)
}

func TestFailsOnUnreadableFile(t *testing.T) {
	decodedPath := "unreadable"
	unreadableFile := filepath.Join(t.TempDir(), base64.StdEncoding.EncodeToString([]byte(decodedPath)))
	f, err := os.Create(unreadableFile)
	require.NoError(t, err)
	require.NoError(t, f.Chmod(0o200))
	expectedErrPrefix := "Failed opening manifest: " + decodedPath

	err = validateOpsLevelAnnotationsForManifests([]string{unreadableFile})

	require.ErrorContains(t, err, expectedErrPrefix)
}

// encodeManifest basically copies the details of kustomize-build-dirs
// which takes in a path to a kustomize directory and writes the built
// manifests to a file whose name is the directory path base64 encoded
func encodeManifest(t *testing.T, manifestPath string) string {
	t.Helper()

	src, err := os.Open(manifestPath)
	require.NoErrorf(t, err, "failed to open manifest path %s", manifestPath)

	dstPath := filepath.Join(t.TempDir(), base64.StdEncoding.EncodeToString([]byte(manifestPath)))
	dst, err := os.Create(dstPath)
	require.NoError(t, err, "failed to create temp manifest")

	_, err = io.Copy(dst, src)
	require.NoError(t, err, "failed to copy manifest")

	return dstPath
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

	encodedPath := encodeManifest(t, manifestPath)
	err := validateOpsLevelAnnotationsForManifests([]string{encodedPath})

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
				encodedPath := encodeManifest(t, manifest)

				gotErr := validateOpsLevelAnnotationsForManifests([]string{encodedPath})
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
			encodedPath := encodeManifest(t, manifest)
			require.NoError(t, validateOpsLevelAnnotationsForManifests([]string{encodedPath}))
		})
	}
}
