.PHONY: test test-race vet coverage

test:
	go test ./...

test-race:
	go test -race ./...

vet:
	go vet ./...

coverage:
	./scripts/check-coverage.sh
