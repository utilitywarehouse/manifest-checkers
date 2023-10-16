# Public Applications

This directory contains applications that may be used outside of this
repository. For that reason they should be versioned (following semantic
versioning)

## Releasing

A tag of the form `cmd/<application-name>/<version>` will trigger a job that
will:

  - Build binaries of the given application
  - Create a release using the given tag and with the binaries as assets

For example, pushing a tag `cmd/kustomize-build-dirs/v12.3.4` would create a
release of [`kustomize-build-dirs`](./kustomize-build-dirs).
