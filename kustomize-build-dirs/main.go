/*
kustomize-build-dirs takes a list of filenames, and for each one walks up the
directory tree until it finds a directory containing `kustomization.yaml` then
runs `kustomize build` on that directory, saving the output in the directory
given by `--out-dir`.

This program should only be run from the root of a Git repository.

Usage:

	kustomize-build-dirs --out-dir <dir> <filenames ... >

Example:

	git diff --diff-filter d --name-only main | xargs kustomize-build-dirs --out-dir manifests/ --

For each kustomize directory the directory tree from the repo root to that
directory will be constructed in the output dir and the built manifests stored
in 'manifests.yaml' there. For example, if there is a kustomize directory at
'project-manifests', then running

	kustomize-build-dirs --out-dir build project-manifests

Will result in the built manifests being placed at
'build/project-manifests/manifests.yaml'

To avoid requiring extra broadly scoped credentials this program will empty
any files containing secrets before running `kustomize build`. So the contents
of any secrets will not be present in the output.
*/
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/urfave/cli/v2"
	"golang.org/x/sync/errgroup"
)

const manifestFileName = "manifests.yaml"

// variable used for testing
var getwdFunc = os.Getwd

func main() {
	var opts struct {
		outDir string
	}
	app := &cli.App{
		Name:  "kustomize-build-dirs",
		Usage: "Given a list of input files, run `kustomize build` somewhere",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:        "out-dir",
				Required:    true,
				Usage:       "Directory to output build manifests",
				Destination: &opts.outDir,
			},
		},
		Action: func(c *cli.Context) error {
			return kustomizeBuildDirs(opts.outDir, c.Args().Slice())
		},
	}

	if err := app.Run(os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func kustomizeBuildDirs(outDir string, filepaths []string) error {
	rootDir, err := getwdFunc()
	if err != nil {
		return fmt.Errorf("error reading working directory: %v", err)
	}

	if err = checkKustomizeInstalled(); err != nil {
		return err
	}

	kustomizationRoots, err := findKustomizationRoots(rootDir, filepaths)
	if err != nil {
		return err
	}

	// truncate secrets so we can run `kustomize build` without having to decrypt them
	if err := truncateSecrets(rootDir, kustomizationRoots); err != nil {
		return err
	}

	manifestMap, err := buildManifests(kustomizationRoots, rootDir)
	if err != nil {
		return err
	}

	for manifestPath, manifest := range manifestMap {
		if err := writeManifest(manifest, outDir, manifestPath); err != nil {
			return err
		}
	}

	return nil
}

func checkKustomizeInstalled() error {
	if _, err := exec.LookPath("kustomize"); err != nil {
		return errors.New(
			"requires `kustomize` to be installed https://kubectl.docs.kubernetes.io/installation/kustomize/",
		)
	}
	return nil
}

// findKustomizationRoots finds, for each given path, the first parent
// directory containing a 'kustomization.yaml'. It returns a list of such paths
// relative to the root
func findKustomizationRoots(root string, paths []string) ([]string, error) {
	// there may be multiple changes under the same path
	// so use a map to track unique ones
	rootsMap := map[string]struct{}{}
	for _, path := range paths {
		kustomizationRoot, err := findKustomizationRoot(root, path)
		if err != nil {
			return nil, err
		}
		if kustomizationRoot == "" {
			continue
		}
		if _, exists := rootsMap[kustomizationRoot]; !exists {
			fmt.Printf("Found kustomization build dir: %s\n", kustomizationRoot)
			rootsMap[kustomizationRoot] = struct{}{}
		}
	}

	roots := make([]string, 0, len(rootsMap))
	for root := range rootsMap {
		roots = append(roots, root)
	}
	return roots, nil
}

func findKustomizationRoot(repoRoot string, relativePath string) (string, error) {
	for dir := filepath.Dir(relativePath); dir != ".."; dir = filepath.Clean(filepath.Join(dir, "..")) {
		_, err := os.Stat(filepath.Join(repoRoot, dir, "kustomization.yaml"))
		switch {
		case err == nil:
			// found 'kustomization.yaml'
			return dir, nil
		case err != nil && !os.IsNotExist(err):
			return "", fmt.Errorf("Error checking for file in %s: %v", dir, err)
		default:
			// file not found, continue up the directory tree
			continue
		}
	}
	return "", nil
}

func truncateSecrets(rootDir string, dirs []string) error {
	secrets, err := findSecrets(rootDir, dirs)
	if err != nil {
		return err
	}

	for _, secret := range secrets {
		file, err := os.OpenFile(filepath.Join(rootDir, secret), os.O_TRUNC, 0o002)
		if err != nil {
			return fmt.Errorf("error truncating secrets file '%s': %v", secret, err)
		}
		defer file.Close()
	}

	return nil
}

// findSecrets finds files under rootDir, that is assumed to be within a git
// repo, that appear to be strongbox encoded secrets
func findSecrets(rootDir string, dirs []string) ([]string, error) {
	// files that look to be strongbox encrypted based on their git attributes
	// docs https://git-scm.com/docs/gitglossary#Documentation/gitglossary.txt-aiddefpathspecapathspec
	encryptedPathspec := ":(attr:filter=strongbox diff=strongbox)"
	pathspecs := make([]string, len(dirs))
	for i, dir := range dirs {
		pathspecs[i] = encryptedPathspec + dir
	}

	var stdout strings.Builder
	var stderr strings.Builder
	// "-z" to use null byte as field terminator, in case someone creates a
	// file with a "\n" in the name (for some reason)
	// we prepend the pathspec 'not/a/path' that will match nothing to:
	//	1) Avoid matching everything when dirs is empty
	//	2) To work around a bug in 'ls-files': https://lore.kernel.org/git/CAEzX-aD1wfgp8AvNNfCXVM3jAaAjK+uFTqS2XP4CJbVvFr2BtQ@mail.gmail.com/
	args := append([]string{"-C", rootDir, "ls-files", "-z", "--", "not/a/path"}, pathspecs...)
	cmd := exec.Command("git", args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf(
			"Error listing secrets via 'git %s': %v\nstderr: %s",
			strings.Join(args, " "),
			err,
			stderr.String(),
		)
	}

	secrets := strings.Split(stdout.String(), "\x00")
	// there's always a trailing '\x00' so trim that element
	return secrets[:len(secrets)-1], nil
}

func buildManifests(kustomizationRoots []string, rootDir string) (map[string]string, error) {
	// `kustomize build` can take some time to run, particularly if it needs to
	// fetch some remote resources, so call it concurrently
	group := new(errgroup.Group)
	mutex := new(sync.Mutex)
	manifestMap := make(map[string]string, len(kustomizationRoots))
	for i := range kustomizationRoots {
		kustomizationRoot := kustomizationRoots[i]
		fmt.Printf("Running `kustomize build %s`\n", kustomizationRoot)
		group.Go(func() error {
			manifest, err := kustomizeBuild(filepath.Join(rootDir, kustomizationRoot))
			if err != nil {
				return err
			}
			mutex.Lock()
			fmt.Printf("Built: %s\n", kustomizationRoot)
			manifestMap[kustomizationRoot] = manifest
			defer mutex.Unlock()
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return nil, err
	}
	return manifestMap, nil
}

func kustomizeBuild(path string) (string, error) {
	var stdout strings.Builder
	var stderr strings.Builder
	args := []string{"build", path}
	cmd := exec.Command("kustomize", args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf(
			"Error running 'kustomize %s': %v\nstderr: %s",
			strings.Join(args, " "),
			err,
			stderr.String(),
		)
	}

	return stdout.String(), nil
}

func writeManifest(manifest string, outDir string, manifestPath string) error {
	targetDir := filepath.Join(outDir, manifestPath)
	if err := os.MkdirAll(targetDir, 0o700); err != nil {
		return fmt.Errorf("failed creating target directory '%s': %v", targetDir, err)
	}
	target := filepath.Join(targetDir, manifestFileName)

	if err := os.WriteFile(target, []byte(manifest), 0o600); err != nil {
		return fmt.Errorf("error writing to '%s': %v", target, err)
	}
	return nil
}
