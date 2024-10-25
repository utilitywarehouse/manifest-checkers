package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type change struct {
	status string
	files  []string
}

func Test_KustomizeRootDetection(t *testing.T) {
	files := []string{
		"dev-aws/first-namespace/kustomization.yaml",
		"dev-aws/first-namespace/first-service/kustomization.yaml",
		"dev-aws/first-namespace/first-service/deployment.yaml",
		"dev-aws/first-namespace/first-service/config/config.yaml",
		"dev-aws/first-namespace/second-service/kustomization.yaml",
		"dev-aws/first-namespace/second-service/cronjob.yaml",

		"dev-aws/second-namespace/kustomization.yaml",
		"dev-aws/second-namespace/first-service/kustomization.yaml",
		"dev-aws/second-namespace/first-service/deployment.yaml",
		"dev-aws/second-namespace/first-service/config/nested/config.yaml",

		"prod-aws/first-namespace/kustomization.yaml",
		"prod-aws/first-namespace/first-service/kustomization.yaml",
		"prod-aws/first-namespace/first-service/deployment.yaml",
		"prod-aws/first-namespace/first-service/config/config.yaml",
		"prod-aws/first-namespace/second-service/kustomization.yaml",
		"prod-aws/first-namespace/second-service/cronjob.yaml",
	}
	buildRoot := setupFiles(t, files)

	for _, tc := range []struct {
		desc          string
		changes       []change
		expectedBases []string
	}{
		{
			desc: "base of edit of single kustomization is that kustomization",
			changes: []change{
				{
					status: changeStatusModification,
					files:  []string{"dev-aws/first-namespace/first-service/kustomization.yaml"},
				},
			},
			expectedBases: []string{"dev-aws/first-namespace/first-service"},
		},
		{
			desc: "base of edit of single non-kustomization is that nearest kustomization",
			changes: []change{
				{
					status: changeStatusModification,
					files:  []string{"dev-aws/first-namespace/first-service/deployment.yaml"},
				},
			},
			expectedBases: []string{"dev-aws/first-namespace/first-service"},
		},
		{
			desc: "base of adding of single non-kustomization is that nearest kustomization",
			changes: []change{
				{
					status: changeStatusAddition,
					files:  []string{"dev-aws/first-namespace/first-service/deployment.yaml"},
				},
			},
			expectedBases: []string{"dev-aws/first-namespace/first-service"},
		},
		{
			desc: "base of adding a kustomization is the nearest parent kustomization",
			changes: []change{
				{
					status: changeStatusAddition,
					files:  []string{"dev-aws/first-namespace/first-service/kustomization.yaml"},
				},
			},
			expectedBases: []string{"dev-aws/first-namespace"},
		},
		{
			desc: "base of deleting a single non-kustomization is that nearest kustomization",
			changes: []change{
				{
					status: changeStatusDeletion,
					files:  []string{"dev-aws/first-namespace/first-service/deployment.yaml"},
				},
			},
			expectedBases: []string{"dev-aws/first-namespace/first-service"},
		},
		{
			desc: "base of deleting a single non-kustomization is that nearest kustomization",
			changes: []change{
				{
					status: changeStatusDeletion,
					files:  []string{"dev-aws/first-namespace/first-service/not-a-file.yaml"},
				},
			},
			expectedBases: []string{"dev-aws/first-namespace/first-service"},
		},
		{
			desc: "base of deleting a kustomization is the nearest parent kustomization",
			changes: []change{
				{
					status: changeStatusAddition,
					files:  []string{"dev-aws/first-namespace/first-service/kustomization.yaml"},
				},
			},
			expectedBases: []string{"dev-aws/first-namespace"},
		},
		{
			desc: "base of deleting a kustomization with no parents is empty",
			changes: []change{
				{
					status: changeStatusAddition,
					files:  []string{"dev-aws/first-namespace/kustomization.yaml"},
				},
			},
			expectedBases: []string{},
		},
		{
			desc: "base of copying a single non-kustomization is that nearest kustomization",
			changes: []change{
				{
					status: changeStatusCopy,
					files: []string{
						"dev-aws/first-namespace/first-service/deployment.yaml",
						"dev-aws/first-namespace/first-service/deployment2.yaml",
					},
				},
			},
			expectedBases: []string{"dev-aws/first-namespace/first-service"},
		},
		{
			desc: "base of copying a kustomization is that nearest parent kustomization",
			changes: []change{
				{
					status: changeStatusCopy,
					files: []string{
						"dev-aws/first-namespace/first-service/kustomization.yaml",
						"dev-aws/second-namespace/first-service/kustomization.yaml",
					},
				},
			},
			expectedBases: []string{"dev-aws/second-namespace"},
		},
		{
			desc: "base of renaming file is base of both source and destination",
			changes: []change{
				{
					status: changeStatusRenaming,
					files: []string{
						"dev-aws/first-namespace/first-service/deployment.yaml",
						"dev-aws/first-namespace/first-service/better-named.yaml",
					},
				},
			},
			expectedBases: []string{
				"dev-aws/first-namespace/first-service",
				"dev-aws/first-namespace/first-service",
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			diffTreeOutput := buildDiffTreeOutput(tc.changes)

			out := &bytes.Buffer{}
			in := strings.NewReader(diffTreeOutput)
			ctx := context.Background()
			args := []string{"exec-name", "--build-root", buildRoot}

			err := runApp(ctx, out, in, args)
			require.NoError(t, err)

			if len(tc.expectedBases) == 0 {
				// distinguish between: no output produced vs. printing ""
				require.Equal(t, 0, out.Len())
			} else {
				outStr := out.String()
				bases := strings.Split(strings.TrimSuffix(outStr, "\n"), "\n")
				require.Equal(t, tc.expectedBases, bases)
			}
		})
	}
}

func setupFiles(t *testing.T, files []string) string {
	t.Helper()
	testDir := t.TempDir()

	for _, filename := range files {
		normPath := filepath.Join(strings.Split(filename, "/")...)
		fullPath := filepath.Join(testDir, normPath)
		dir := filepath.Dir(fullPath)

		require.NoError(t, os.MkdirAll(dir, 0o700))
		_, err := os.Create(fullPath)
		require.NoError(t, err)
	}

	return testDir
}

func buildDiffTreeOutput(changes []change) string {
	// placeholders for parts of the output we aren't interested in
	sha := "cb3d1f86f0a5e165ca32e1d9ea8eb748d1ee78d4"
	perms := "100644"

	res := ""
	for _, change := range changes {
		line := fmt.Sprintf(
			":%[1]s %[1]s %[2]s %[2]s %[3]s\t%[4]s",
			perms,
			sha,
			change.status,
			strings.Join(change.files, "\t"),
		)
		res += line + "\n"
	}

	return res
}
