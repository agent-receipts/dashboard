.PHONY: build test lint run clean

build:
	go build -o dashboard ./cmd/dashboard

test:
	go test ./... -count=1

lint:
	go vet ./...

run: build
	./dashboard --db $(DB)

clean:
	rm -f dashboard
