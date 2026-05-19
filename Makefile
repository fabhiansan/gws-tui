.PHONY: install build test check clean

install:
	go install ./cmd/gws

build:
	go build -o ./bin/gws ./cmd/gws

test:
	go test ./...

check:
	go vet ./...
	go test ./...

clean:
	rm -rf ./bin
