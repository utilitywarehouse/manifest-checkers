# `get-kustomize-build-base`

Command to find the build base to run `kustomize build` provided some changes
from git. Specifically, this command expects to be fed the output of `git diff
--raw` like:

    git diff --raw HEAD^1 HEAD | get-kustomimze-build-base --build-root "$PWD"

Where `HEAD` is a merge commit or a squashed commit representing a pull request.
The output depends on the type of change for each file in the output. For
modifications it will walk up the directory tree until the nearest
`kustomization.yaml`. For additions or deletions, if the file is not a
`kustomization.yaml` it will do the same as for a modification, otherwise it
will walk up the directory, skipping the current directory, until it finds a
`kustomization.yaml` (i.e. a parent build directory that should exist on both
sides of the change)

It does **not** (yet):

  - Deduplicate its output, e.g. if two files under a single build directory are
    change then that directory will be printed twice
  - Handle deleting a build directory and its parent, in this case when
    processing the child build directory it will print the parent directory
    (which wouldn't exist on the other side of the diff).
