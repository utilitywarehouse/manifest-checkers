# Manifest Checkers

A collection of applications used to help build and check Kubernetes manifests
at Utility Warehouse.

## `kustomize-build-dirs`

kustomize-build-dirs takes a list of filenames, and for each one walks up the
directory tree until it finds a directory containing `kustomization.yaml` then
runs `kustomize build` on that directory, saving the output in the directory
given by `--out-dir`.

This program should only be run from the root of a Git repository.

Usage:

    NAME:
       kustomize-build-dirs - Given a list of input files, run `kustomize build` somewhere
    
    USAGE:
       kustomize-build-dirs [global options] command [command options] 
    
    COMMANDS:
       help, h  Shows a list of commands or help for one command
    
    GLOBAL OPTIONS:
       --out-dir value     Directory to output build manifests
       --truncate-secrets  Whether or not to truncate secrets. This can make life easier when you don't have strongbox credentials for some secrets (default: false)
       --help, -h          show help

Example:

    git diff --diff-filter d --name-only main | xargs kustomize-build-dirs --out-dir manifests/ --

For each kustomize directory the directory tree from the repo root to that
directory will be constructed in the output dir and the built manifests stored
in 'manifests.yaml' there. For example, if there is a kustomize directory at
'project-manifests', then running

    kustomize-build-dirs --out-dir build project-manifests

Will result in the built manifests being placed at
'build/project-manifests/manifests.yaml'

Passing the `--truncate-secrets` flag will cause the application to empty any
files that look to be [`strongbox`](https://github.com/uw-labs/strongbox)
encrypted before running `kustomize build`, so the contents of any secrets will
not be present in the output. This may be useful to avoid requiring extra
broadly scoped credentials in e.g. CI environments which wouldn't otherwise need
them.

## `validate-opslevel-annotations`

`validate-opslevel-annotations` checks the OpsLevel annotations for a list of
manifests are valid against what our [`opslevel`](https://www.opslevel.com/)
setup expects..

It will process an entire manifest file and report on all errors across all
objects therein, though it will bail upon the first manifest it can't read or
can't interpret.

Usage:

    validate-opslevel-annotations [ manifest-file] ...
