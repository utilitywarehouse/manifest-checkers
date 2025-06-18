package main

import (
	"errors"
	"fmt"
	"io/fs"
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
	mockdirDepth        = 0
	simpleKustomization = `apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

resources:
  - deployment.yaml
`

	componentKustomization = `apiVersion: kustomize.config.k8s.io/v1beta1
kind: Component

patches:
  - path: deployment.yaml
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
	require.LessOrEqual(
		t,
		len(prefix),
		len(err.Error()),
		fmt.Sprintf("error cannot be shorter than prefix, err: %s, prefix: %s", err, prefix),
	)
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
	// avoid loading any user/system specific configuration
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null")

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

	err := kustomizeBuildDirs(mockoutDir, mockdirDepth, false, []string{})

	require.EqualError(t, err, expectedError)
}

func TestFailsWhenUnableToFindKustomize(t *testing.T) {
	expectedError := "requires `kustomize` to be installed https://kubectl.docs.kubernetes.io/installation/kustomize/"
	t.Setenv("PATH", "")
	err := kustomizeBuildDirs(mockoutDir, mockdirDepth, false, []string{})

	require.EqualError(t, err, expectedError)
}

func TestFailsWhenUnableToListSecrets(t *testing.T) {
	workDir := t.TempDir()
	setwd(t, workDir)
	_, err := os.Create(filepath.Join(workDir, "kustomization.yaml"))
	require.NoError(t, err)

	expectedErrPrefix := fmt.Sprintf(
		"Error listing secrets via 'git -C %s ls-files -z -- not/a/path :(attr:filter=strongbox diff=strongbox).'",
		workDir,
	)

	// run command outside any Git directory
	err = kustomizeBuildDirs(mockoutDir, mockdirDepth, true, []string{"kustomization.yaml"})
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

	err := kustomizeBuildDirs(mockoutDir, mockdirDepth, true, []string{"kustomization.yaml"})

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
	expectedErrPrefix := "error checking for file in manifests:"

	err := kustomizeBuildDirs(
		mockoutDir,
		mockdirDepth,
		false,
		[]string{"manifests/kustomization.yaml"},
	)
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

	err := kustomizeBuildDirs(mockoutDir, mockdirDepth, false, []string{kustomizationPath})

	requireErorrPrefix(t, err, expectedErrPrefix)
}

func TestFailsWhenUnableToCreateTargetDir(t *testing.T) {
	gitDir := t.TempDir()
	setwd(t, gitDir)
	unwritableDir := mockoutDir
	manifestDir := "manifests"
	expectedErrPrefix := fmt.Sprintf(
		"failed creating target directory '%s'",
		filepath.Join(unwritableDir, manifestDir),
	)

	deploymentPath := filepath.Join(manifestDir, "deployment.yaml")
	repoFiles := map[string]string{
		filepath.Join("manifests", "kustomization.yaml"): simpleKustomization,
		deploymentPath: simpleDeployment,
	}
	buildGitRepo(t, gitDir, repoFiles)

	err := kustomizeBuildDirs(unwritableDir, mockdirDepth, false, []string{deploymentPath})
	requireErorrPrefix(t, err, expectedErrPrefix)
}

func setupTest(t *testing.T) (string, string) {
	t.Helper()
	gitDir := t.TempDir()
	setwd(t, gitDir)
	outDir := filepath.Join(gitDir, "outdir")

	return gitDir, outDir
}

func TestFailsWhenFailingToWriteManifest(t *testing.T) {
	gitDir, outDir := setupTest(t)
	manifestDir := "manifests"
	buildDir := filepath.Join(outDir, manifestDir)
	require.NoError(t, os.Mkdir(outDir, 0o700))
	require.NoError(t, os.Mkdir(buildDir, 0o500))
	expectedErrPrefix := fmt.Sprintf(
		"error writing to '%s'",
		filepath.Join(buildDir, manifestFileName),
	)

	deploymentPath := filepath.Join(manifestDir, "deployment.yaml")
	repoFiles := map[string]string{
		filepath.Join("manifests", "kustomization.yaml"): simpleKustomization,
		deploymentPath: simpleDeployment,
	}
	buildGitRepo(t, gitDir, repoFiles)

	err := kustomizeBuildDirs(outDir, mockdirDepth, false, []string{deploymentPath})
	requireErorrPrefix(t, err, expectedErrPrefix)
}

func TestDoesNothingWhenNothingToBuild(t *testing.T) {
	gitDir, outDir := setupTest(t)

	repoFiles := map[string]string{
		"README.md": "# Readme\nThis is a repo\n",
		filepath.Join("manifests", "kustomization.yaml"): simpleKustomization,
		filepath.Join("manifests", "deployment.yaml"):    simpleDeployment,
	}
	buildGitRepo(t, gitDir, repoFiles)

	require.NoError(t, kustomizeBuildDirs(outDir, mockdirDepth, true, []string{"README.md"}))

	require.NoFileExists(t, outDir)
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
		"secrets.yaml": "encryptedNonsensehere\xff\xff\n",
		".gitattributes": fmt.Sprintf(
			"%s	filter=strongbox diff=strongbox\n",
			"secrets.yaml",
		),
		"kustomization.yaml": kustomizationContent,
	}

	buildGitRepo(t, gitDir, repoFiles)
	require.NoError(
		t,
		kustomizeBuildDirs(outDir, mockdirDepth, true, []string{"kustomization.yaml"}),
	)
}

func readOutDir(t *testing.T, outDir string) map[string]string {
	t.Helper()

	outfileContents := map[string]string{}
	walkFunc := func(path string, info fs.FileInfo, err error) error {
		require.NoErrorf(t, err, "unexpected error on %s when walking %s", path, outDir)
		if info.Name() == manifestFileName {
			contents, err := os.ReadFile(path)
			require.NoError(t, err, "unexpected error when reading %s", path)

			outfileContents[path] = string(contents)
		}
		return nil
	}

	require.NoError(t, filepath.Walk(outDir, walkFunc))

	return outfileContents
}

func compareResults(
	t *testing.T,
	outDir string,
	expected map[string]string,
	got map[string]string,
) {
	for relPath, expectedManifest := range expected {
		expectedPath := filepath.Join(outDir, relPath, manifestFileName)
		gotManifest, ok := got[expectedPath]

		require.Truef(t, ok, "missing manifest for '%s'", relPath)
		require.Equal(t, expectedManifest, gotManifest)
	}
}

func TestDontRenderComponent(t *testing.T) {
	gitDir, outDir := setupTest(t)

	manifestPath := filepath.Join("manifests", "deployment.yaml")
	repoFiles := map[string]string{
		filepath.Join("manifests", "kustomization.yaml"): componentKustomization,
		manifestPath: simpleDeployment,
	}
	buildGitRepo(t, gitDir, repoFiles)

	require.NoError(t, kustomizeBuildDirs(outDir, mockdirDepth, false, []string{manifestPath}))
	require.NoFileExists(t, outDir)
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

	require.NoError(t, kustomizeBuildDirs(outDir, mockdirDepth, false, []string{manifestPath}))
	compareResults(t, outDir, expectedContents, readOutDir(t, outDir))
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

	require.NoError(t, kustomizeBuildDirs(outDir, mockdirDepth, false, []string{nonManifestPath}))
	compareResults(t, outDir, expectedContents, readOutDir(t, outDir))
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

	require.NoError(
		t,
		kustomizeBuildDirs(
			outDir,
			mockdirDepth,
			false,
			[]string{firstDeploymentPath, secondDeploymentPath},
		),
	)
	compareResults(t, outDir, expectedContents, readOutDir(t, outDir))
}

func TestWriteMultipleManifestsOneIscomponent(t *testing.T) {
	gitDir, outDir := setupTest(t)

	firstDeploymentPath := filepath.Join("first-project", "deployment.yaml")
	firstDeploymentcontent := fmt.Sprintf(simpleDeploymentTemplate, "first-app")
	secondDeploymentPath := filepath.Join("second-project", "deployment.yaml")
	secondDeploymentcontent := fmt.Sprintf(simpleDeploymentTemplate, "second-app")
	repoFiles := map[string]string{
		firstDeploymentPath: firstDeploymentcontent,
		filepath.Join("first-project", "kustomization.yaml"): simpleKustomization,
		secondDeploymentPath: secondDeploymentcontent,
		filepath.Join("second-project", "kustomization.yaml"): componentKustomization,
	}
	buildGitRepo(t, gitDir, repoFiles)
	expectedContents := map[string]string{
		"first-project": firstDeploymentcontent,
	}

	require.NoError(
		t,
		kustomizeBuildDirs(
			outDir,
			mockdirDepth,
			false,
			[]string{firstDeploymentPath, secondDeploymentPath},
		),
	)
	compareResults(t, outDir, expectedContents, readOutDir(t, outDir))
}

func TestSecretsStubbed(t *testing.T) {
	gitDir, outDir := setupTest(t)
	manifestsDir := filepath.Join("src", "manifests")

	secretContent := "t0p-s3cret"
	deploymentContent := fmt.Sprintf(simpleDeploymentTemplate, "my-app")
	kustomizationContent := `kind: Kustomization
resources:
  - deployment.yaml
  - first-secret.yaml
  - second-secret.yaml
`
	expectedContent := deploymentContent

	repoFiles := map[string]string{
		filepath.Join(manifestsDir, "deployment.yaml"):    deploymentContent,
		filepath.Join(manifestsDir, "first-secret.yaml"):  secretContent,
		filepath.Join(manifestsDir, "second-secret.yaml"): secretContent,
		filepath.Join(manifestsDir, "kustomization.yaml"): kustomizationContent,
		// nesting '.gitattributes' under a directory tests the 'git ls-files'
		// bug mentioned in findSecrets
		filepath.Join(manifestsDir, ".gitattributes"): "*-secret.yaml	filter=strongbox diff=strongbox\n",
	}
	buildGitRepo(t, gitDir, repoFiles)
	expectedContents := map[string]string{
		manifestsDir: expectedContent,
	}

	require.NoError(
		t,
		kustomizeBuildDirs(
			outDir,
			mockdirDepth,
			true,
			[]string{filepath.Join(manifestsDir, "kustomization.yaml")},
		),
	)
	compareResults(t, outDir, expectedContents, readOutDir(t, outDir))
}

func TestDeepestCommonDirs(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		minDepth int
		expected []string
	}{
		{
			name:     "empty input",
			input:    []string{},
			minDepth: 0,
			expected: nil,
		},
		{
			name:     "single path, depth 0",
			input:    []string{"aaa/bbb/ccc/file.yaml"},
			minDepth: 0,
			expected: []string{"aaa/bbb/ccc/"},
		},
		{
			name: "multiple grouped paths",
			input: []string{
				"file.yaml",
				"aaa/bbb/ccc/file.yaml",
				"aaa/bbb/ccc/bbb/file.yaml",
				"aaa/bbb/ccc/ddd/file1.yaml",
				"aaa/bbb/ccc/ddd/file2.yaml",
				"aaa/bbb/ccc/ddd/eee/file.yaml",
				"bbb/ccc/file.yaml",
				"bbb/ccc/ddd/file.yaml",
				"ccc/ddd/eee/fff/ggg/hhh/file.yaml",
			},
			minDepth: 1,
			expected: []string{
				"aaa/bbb/ccc/",
				"bbb/ccc/",
				"ccc/ddd/eee/fff/ggg/hhh/",
			},
		},
		{
			name: "no shared prefixes",
			input: []string{
				"aaa/file.yaml",
				"bbb/file.yaml",
				"ccc/file.yaml",
			},
			minDepth: 1,
			expected: []string{
				"aaa/",
				"bbb/",
				"ccc/",
			},
		},
		{
			name: "nested and flat mix",
			input: []string{
				"aaa/bbb/ccc/file.yaml",
				"aaa/bbb/file.yaml",
				"bbb/ccc/file.yaml",
			},
			minDepth: 1,
			expected: []string{
				"aaa/bbb/",
				"bbb/ccc/",
			},
		},
		{
			name:     "single file in root dir",
			input:    []string{"file.yaml"},
			minDepth: 0,
			expected: []string{""},
		},
		{
			name: "multiple files, depth 0",
			input: []string{
				"file.yaml",
				"aaa/bbb/file.yaml",
				"ccc/file.yaml",
			},
			minDepth: 0,
			expected: []string{
				"",
				"aaa/bbb/",
				"ccc/",
			},
		},
		{
			name: "multiple files, depth 1",
			input: []string{
				"file.yaml",
				"aaa/bbb/file.yaml",
				"ccc/file.yaml",
			},
			minDepth: 1,
			expected: []string{
				"aaa/bbb/",
				"ccc/",
			},
		},
		{
			name: "multiple files, depth 2",
			input: []string{
				"file.yaml",
				"aaa/file.yaml",
				"aaa/bbb/file.yaml",
				"bbb/ccc/file.yaml",
			},
			minDepth: 2,
			expected: []string{
				"aaa/bbb/",
				"bbb/ccc/",
			},
		},
		{
			name: "multiple files, depth 2",
			input: []string{
				"file.yaml",
				"aaa/bbb/file.yaml",
				"aaa/bbb/ccc/file.yaml",
				"aaa/bbb/ccc/ddd/file.yaml",
			},
			minDepth: 3,
			expected: []string{
				"aaa/bbb/ccc/",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := deepestCommonDirs(tt.input, tt.minDepth)
			require.Equal(t, tt.expected, got)
		})
	}
}
