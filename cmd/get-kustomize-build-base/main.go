package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/urfave/cli/v2"
)

// see https://git-scm.com/docs/git-diff#_raw_output_format
const (
	changeStatusModification = "M"
	changeStatusChangingType = "T"
	changeStatusAddition     = "A"
	changeStatusDeletion     = "D"
	changeStatusCopy         = "C"
	changeStatusRenaming     = "R"
	// not relevant: U and X

	gitDiffStatusColumn = 4
)

func main() { //go-cov:skip
	if err := runApp(context.Background(), os.Stdout, os.Stdin, os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runApp(ctx context.Context, out io.Writer, in io.Reader, args []string) error {
	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGINT)
	defer cancel()

	app := buildApp(out, in)

	return app.RunContext(ctx, args)
}

func buildApp(out io.Writer, in io.Reader) cli.App {
	return cli.App{
		Name:  "get-kustomize-build-base",
		Usage: "Print the list of build bases for kustomize given the output of `git diff --raw` provided via stdin",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "build-root",
				Required: true,
			},
		},
		Action: func(c *cli.Context) error {
			return run(c.Context, out, in, c.String("build-root"))
		},
	}
}

func run(_ context.Context, out io.Writer, in io.Reader, buildRoot string) error {
	scanner := bufio.NewScanner(in)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Split(line, " ")
		// files are listed directly after the status
		statusAndFiles := strings.Split(parts[gitDiffStatusColumn], "\t")
		status := statusAndFiles[0]
		files := statusAndFiles[1:]
		kustomizeBase, err := getBaseForChange(buildRoot, status, files)
		if err != nil { //go-cov:skip
			return err
		}
		if kustomizeBase != "" {
			fmt.Fprintln(out, kustomizeBase)
		}
	}
	if err := scanner.Err(); err != nil { //go-cov:skip
		return fmt.Errorf("scanning input: %w", err)
	}

	return nil
}

func getBaseForChange(buildRoot string, status string, files []string) (string, error) {
	switch status {
	case changeStatusModification, changeStatusChangingType:
		return findKustomizationRoot(buildRoot, files[0])
	case changeStatusAddition, changeStatusDeletion:
		return findKustomizationRootForNew(buildRoot, files[0])
	case changeStatusCopy:
		dst := files[1]
		return findKustomizationRootForNew(buildRoot, dst)
	case changeStatusRenaming:
		// a rename is just a delete+an add
		src := files[0]
		deleted, err := findKustomizationRootForNew(buildRoot, src)
		if err != nil { //go-cov:skip
			return "", err
		}

		dst := files[1]
		added, err := findKustomizationRootForNew(buildRoot, dst)
		if err != nil { //go-cov:skip
			return "", err
		}
		// bit of a hack to return multiple lines...
		return deleted + "\n" + added, nil
	default: //go-cov:skip
		return "", nil
	}
}

func findKustomizationRootForNew(buildRoot string, relativePath string) (string, error) {
	var searchRoot string
	if filepath.Base(relativePath) == "kustomization.yaml" {
		// TODO: what if we've also added/removed this root?
		searchRoot = filepath.Dir(relativePath)
	} else {
		searchRoot = relativePath
	}
	return findKustomizationRoot(buildRoot, searchRoot)
}

func findKustomizationRoot(buildRoot string, relativePath string) (string, error) {
	for dir := filepath.Dir(relativePath); dir != ".."; dir = filepath.Clean(filepath.Join(dir, "..")) {
		_, err := os.Stat(filepath.Join(buildRoot, dir, "kustomization.yaml"))
		if err != nil { //go-cov:skip
			if errors.Is(err, fs.ErrNotExist) {
				// file not found, continue up the directory tree
				continue
			}
			return "", fmt.Errorf("stating directory %s: %w", dir, err)
		}
		return dir, nil
	}
	return "", nil
}
