package main

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	// placeholder for tests that don't need to write anywhere
	mockoutDir          = "/some/dir"
	simpleKustomization = `apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

resources:
  - deployment.yaml
`
	simpleDeploymentTemplate = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: %s
`
)

var simpleDeployment = fmt.Sprintf(simpleDeploymentTemplate, "my-cool-app")

func setwd(t *testing.T, dir string) {
	orig := getwdFunc

	getwd := func() (string, error) { return dir, nil }
	getwdFunc = getwd

	t.Cleanup(func() { getwdFunc = orig })
}

func requireErorrPrefix(t *testing.T, err error, prefix string) {
	t.Helper()

	require.Error(t, err)
	require.LessOrEqual(t, len(prefix), len(err.Error()), "error cannot be shorter than prefix")
	require.Equalf(t, prefix, err.Error()[:len(prefix)], "full error: %v", err)
}

func buildGitRepo(t *testing.T, gitDir string, fileContents map[string]string) {
	for path, contents := range fileContents {
		require.NoError(t, os.MkdirAll(filepath.Join(gitDir, filepath.Dir(path)), 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(gitDir, path), []byte(contents), 0o600))
	}

	runGitCmd(t, gitDir, []string{"init"})
	runGitCmd(t, gitDir, []string{"add", "."})
}

func runGitCmd(t *testing.T, gitDir string, args []string) {
	var stderr strings.Builder
	args = append([]string{"-C", gitDir}, args...)
	cmd := exec.Command("git", args...)
	cmd.Stderr = &stderr

	require.NoError(
		t,
		cmd.Run(),
		fmt.Sprintf(
			"git command 'git %s' failed: stderr: %s",
			strings.Join(args, " "),
			stderr.String(),
		),
	)
}

func TestFailsWhenUnableToReadWorkingDirectory(t *testing.T) {
	getwdErrorMsg := "Failed to getwd"
	expectedError := fmt.Sprintf("error reading working directory: %s", getwdErrorMsg)
	getwd := func() (string, error) { return "", errors.New(getwdErrorMsg) }

	orig := getwdFunc
	defer func() { getwdFunc = orig }()
	getwdFunc = getwd

	err := kustomizeBuildDirs(mockoutDir, []string{})

	require.EqualError(t, err, expectedError)
}

func TestFailsWhenUnableToFindKustomize(t *testing.T) {
	expectedError := "requires `kustomize` to be installed https://kubectl.docs.kubernetes.io/installation/kustomize/"
	t.Setenv("PATH", "")
	err := kustomizeBuildDirs(mockoutDir, []string{})

	require.EqualError(t, err, expectedError)
}

func TestFailsWhenUnableToListSecrets(t *testing.T) {
	workDir := t.TempDir()
	setwd(t, workDir)
	_, err := os.Create(filepath.Join(workDir, "kustomization.yaml"))
	require.NoError(t, err)

	expectedErrPrefix := fmt.Sprintf(
		"Error listing secrets via 'git -C %s ls-files -z -- :(attr:filter=strongbox diff=strongbox).'",
		workDir,
	)

	// run command outside of any Git directory
	err = kustomizeBuildDirs(mockoutDir, []string{"kustomization.yaml"})
	requireErorrPrefix(t, err, expectedErrPrefix)
}

func TestFailsWhenUnableToTruncateSecret(t *testing.T) {
	gitDir := t.TempDir()
	setwd(t, gitDir)
	secretFile := "secret.json"
	expectedErrPrefix := fmt.Sprintf("error truncating secrets file '%s'", secretFile)

	repoFiles := map[string]string{
		"kustomization.yaml": "\n",
		secretFile:           "secret-content\n",
		".gitattributes":     fmt.Sprintf("%s	filter=strongbox diff=strongbox\n", secretFile),
	}
	buildGitRepo(t, gitDir, repoFiles)
	// make secret file read-only
	require.NoError(t, os.Chmod(filepath.Join(gitDir, secretFile), 0o400))

	err := kustomizeBuildDirs(mockoutDir, []string{"kustomization.yaml"})

	requireErorrPrefix(t, err, expectedErrPrefix)
}

func TestFailsWhenUnableToFindKustomizations(t *testing.T) {
	gitDir := t.TempDir()
	setwd(t, gitDir)
	unredableDirPath := filepath.Join(gitDir, "manifests")

	repoFiles := map[string]string{
		filepath.Join("manifests", "kustomization.yaml"): "\n",
	}
	buildGitRepo(t, gitDir, repoFiles)
	require.NoError(t, os.Chmod(unredableDirPath, 0o600))
	// restore permissions so we can cleanup
	defer os.Chmod(unredableDirPath, 0o700) //nolint:errcheck
	expectedErrPrefix := "Error checking for file in manifests:"

	err := kustomizeBuildDirs(mockoutDir, []string{"manifests/kustomization.yaml"})
	requireErorrPrefix(t, err, expectedErrPrefix)
}

func TestFailsOnKustomizeBuildFailure(t *testing.T) {
	gitDir := t.TempDir()
	setwd(t, gitDir)
	kustomizeDir := "manifests"
	kustomizationPath := filepath.Join(kustomizeDir, "kustomization.yaml")
	badKustomizationContent := "apiVersion: some.other.api/v1\nkind: Kustomization\n"
	repoFiles := map[string]string{
		kustomizationPath: badKustomizationContent,
	}
	buildGitRepo(t, gitDir, repoFiles)
	expectedErrPrefix := fmt.Sprintf(
		"Error running 'kustomize build %s'",
		filepath.Join(gitDir, kustomizeDir),
	)

	err := kustomizeBuildDirs(mockoutDir, []string{kustomizationPath})

	requireErorrPrefix(t, err, expectedErrPrefix)
}

func TestFailsWhenUnableToWriteManifests(t *testing.T) {
	gitDir := t.TempDir()
	setwd(t, gitDir)
	unwritableDir := mockoutDir
	expectedErrPrefix := fmt.Sprintf("error writing to '%s", unwritableDir)

	deploymentPath := filepath.Join("manifests", "deployment.yaml")
	repoFiles := map[string]string{
		filepath.Join("manifests", "kustomization.yaml"): simpleKustomization,
		deploymentPath: simpleDeployment,
	}
	buildGitRepo(t, gitDir, repoFiles)

	err := kustomizeBuildDirs(unwritableDir, []string{deploymentPath})
	requireErorrPrefix(t, err, expectedErrPrefix)
}

func setupTest(t *testing.T) (string, string) {
	t.Helper()
	gitDir := t.TempDir()
	setwd(t, gitDir)
	outDir := filepath.Join(gitDir, "outdir")
	require.NoError(t, os.Mkdir(outDir, 0o700))

	return gitDir, outDir
}

func TestDoesNothingWhenNothingToBuild(t *testing.T) {
	gitDir, outDir := setupTest(t)

	repoFiles := map[string]string{
		"README.md": "# Readme\nThis is a repo\n",
		filepath.Join("manifests", "kustomization.yaml"): simpleKustomization,
		filepath.Join("manifests", "deployment.yaml"):    simpleDeployment,
	}
	buildGitRepo(t, gitDir, repoFiles)

	require.NoError(t, kustomizeBuildDirs(outDir, []string{"README.md"}))

	outFiles, err := os.ReadDir(outDir)
	require.NoError(t, err)
	require.Empty(t, outFiles)
	// sanity check no unexpected truncates
	readmeContent, err := os.ReadFile(filepath.Join(gitDir, "README.md"))
	require.NoError(t, err)
	require.NotEqual(t, string(readmeContent), "non-secret file unexpected truncated")
}

func TestHandlesUnreadableSecrets(t *testing.T) {
	gitDir, outDir := setupTest(t)
	kustomizationContent := `
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

secretGenerator:
  - name: my-secrets
    envs:
      - secrets.yaml
`

	repoFiles := map[string]string{
		"secrets.yaml":       "encryptedNonsensehere\xff\xff\n",
		".gitattributes":     fmt.Sprintf("%s	filter=strongbox diff=strongbox\n", "secrets.yaml"),
		"kustomization.yaml": kustomizationContent,
	}

	buildGitRepo(t, gitDir, repoFiles)
	require.NoError(t, kustomizeBuildDirs(outDir, []string{"kustomization.yaml"}))
}

func readOutDir(t *testing.T, outDir string) map[string]string {
	t.Helper()

	entries, err := os.ReadDir(outDir)
	require.NoError(t, err)

	outfileContents := map[string]string{}
	for _, entry := range entries {
		require.Falsef(t, entry.IsDir(), "unexpected directory in output directory %s", outDir)

		path := filepath.Join(outDir, entry.Name())
		contents, err := os.ReadFile(path)
		require.NoError(t, err)

		name, err := base64.StdEncoding.DecodeString(entry.Name())
		require.NoError(t, err)
		outfileContents[string(name)] = string(contents)
	}
	return outfileContents
}

func TestWriteSingleManifest(t *testing.T) {
	gitDir, outDir := setupTest(t)

	manifestPath := filepath.Join("manifests", "deployment.yaml")
	repoFiles := map[string]string{
		filepath.Join("manifests", "kustomization.yaml"): simpleKustomization,
		manifestPath: simpleDeployment,
	}
	buildGitRepo(t, gitDir, repoFiles)
	expectedContents := map[string]string{
		"manifests": simpleDeployment,
	}

	require.NoError(t, kustomizeBuildDirs(outDir, []string{manifestPath}))
	require.Equal(t, expectedContents, readOutDir(t, outDir))
}

func TestWritesManifestWhenGivenNonManifestFile(t *testing.T) {
	gitDir, outDir := setupTest(t)

	manifestsPath := "manifests"
	nonManifestPath := filepath.Join(manifestsPath, "config", "config.yaml")
	repoFiles := map[string]string{
		nonManifestPath: "someConfigKey: 1\n",
		filepath.Join(manifestsPath, "kustomization.yaml"): simpleKustomization,
		filepath.Join(manifestsPath, "deployment.yaml"):    simpleDeployment,
	}
	buildGitRepo(t, gitDir, repoFiles)
	expectedContents := map[string]string{
		"manifests": simpleDeployment,
	}

	require.NoError(t, kustomizeBuildDirs(outDir, []string{nonManifestPath}))
	require.Equal(t, expectedContents, readOutDir(t, outDir))
}

func TestWriteMultipleManifests(t *testing.T) {
	gitDir, outDir := setupTest(t)

	firstDeploymentPath := filepath.Join("first-project", "deployment.yaml")
	firstDeploymentcontent := fmt.Sprintf(simpleDeploymentTemplate, "first-app")
	secondDeploymentPath := filepath.Join("second-project", "deployment.yaml")
	secondDeploymentcontent := fmt.Sprintf(simpleDeploymentTemplate, "second-app")
	repoFiles := map[string]string{
		firstDeploymentPath: firstDeploymentcontent,
		filepath.Join("first-project", "kustomization.yaml"): simpleKustomization,
		secondDeploymentPath: secondDeploymentcontent,
		filepath.Join("second-project", "kustomization.yaml"): simpleKustomization,
	}
	buildGitRepo(t, gitDir, repoFiles)
	expectedContents := map[string]string{
		"first-project":  firstDeploymentcontent,
		"second-project": secondDeploymentcontent,
	}

	require.NoError(t, kustomizeBuildDirs(outDir, []string{firstDeploymentPath, secondDeploymentPath}))
	require.Equal(t, expectedContents, readOutDir(t, outDir))
}
