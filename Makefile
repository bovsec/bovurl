.PHONY: build test run clean fmt vet

BINARY=bovurl

build:
	go build -o $(BINARY) ./cmd/bovurl

test:
	go test ./... -v

vet:
	go vet ./...

fmt:
	gofmt -l -w .

run: build
	./$(BINARY) $(ARGS)

clean:
	rm -f $(BINARY)
