.PHONY: install build test check clean

install:
	go install .

build:
	go build -o ./bin/gws .

test:
	go test ./...

check:
	go vet ./...
	go test ./...

clean:
	rm -rf ./bin
