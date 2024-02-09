# Contributing

## Releasing

Building binaries and generating releases is managed by
[`goreleaser`](https://goreleaser.com/). A new release will be generated when a
tag of the form `v*` is pushed (these should always be semantic version tags
like `v0.1.0`)

## Linting

Linting is handled via `pre-commit`. Follow the [install
instructions](https://pre-commit.com/#install) then install the hooks:

``` console
$ pre-commit install
$ pre-commit run --all-hooks
```

## Testing

Tests can be run simple via `go test ./...`, though this repo measures (and
enforces) code coverage. To see a report of covered/uncovered lines in your
browser run:

    make report-coverage-html

To check you've covered all lines run:

    make check-coverage

Code coverage is run though [`go-cov`](https://gitlab.com/matthewhughes/go-cov)
so you can skip blocks of code if it's not worthwhile covering them
