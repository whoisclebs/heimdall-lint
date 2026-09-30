.PHONY: build test vet fmt check docker docker-test

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o bin/heimdall ./cmd/heimdall

test:
	go test -race ./...

vet:
	go vet ./...

fmt:
	gofmt -l -w .

check: vet test

docker:
	docker build -t heimdall-lint .

docker-test:
	test/docker/run.sh
