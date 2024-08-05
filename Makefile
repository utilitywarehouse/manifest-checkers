.PHONY: coverage.out
coverage.out:
	@go test -coverprofile $@ ./...

go-cov.out: coverage.out
	@go run gitlab.com/matthewhughes/go-cov/cmd/go-cov add-skips $^ > go-cov.out

.PHONY: report-coverage
report-coverage: go-cov.out
	@go tool cover -func=$^

.PHONY: report-coverage-html
report-coverage-html: go-cov.out
	@go tool cover -html=$^

.PHONY: check-coverage
check-coverage: go-cov.out
	@go run gitlab.com/matthewhughes/go-cov/cmd/go-cov report $^
