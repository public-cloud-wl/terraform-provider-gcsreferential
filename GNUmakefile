default: testacc

# Run acceptance tests
.PHONY: testacc
testacc:
	TF_ACC=1 go test ./... -v $(TESTARGS) -timeout 120m

generate:
	go generate ./...

build:
	go build -v ./...

install:
	go install -v ./...

fmt:
	go fmt ./...

lint:
	golangci-lint run
