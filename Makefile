COVERAGE_MIN := 90
EFFICACY_MIN := 85
MCOVER_MIN := 90

.PHONY: check lint test cover mutate

check: lint cover mutate

lint:
	golangci-lint run ./...

test:
	go test -race ./...

cover:
	go test -race -coverpkg=./internal/... -coverprofile=coverage.out ./...
	@go tool cover -func=coverage.out | awk -v min=$(COVERAGE_MIN) \
		'/^total:/ { sub("%", "", $$3); printf "coverage %s%% (min %s%%)\n", $$3, min; exit ($$3 + 0 < min + 0) }'

mutate:
	gremlins unleash --threshold-efficacy $(EFFICACY_MIN) --threshold-mcover $(MCOVER_MIN) ./...
