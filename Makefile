.PHONY: fmt vet test build run clean

fmt:
	gofmt -l -w .

vet:
	go vet ./...

test:
	go test ./...

build:
	CGO_ENABLED=0 go build -trimpath -o bin/paste ./cmd/paste

run:
	go run ./cmd/paste

clean:
	rm -rf bin
