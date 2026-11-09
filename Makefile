.PHONY: build test dev-api dev-web inspect clean
build:
	npm --prefix web ci --no-audit --no-fund
	npm --prefix web run build
	mkdir -p bin
	CGO_ENABLED=1 go build -trimpath -ldflags='-s -w' -o bin/lab-router ./cmd/lab-router
test:
	go test -race -count=1 ./...
	go vet ./...
dev-api:
	LAB_DATA_DIR=./var LAB_MODE=observation go run ./cmd/lab-router api
dev-web:
	npm --prefix web run dev
inspect:
	./scripts/inspect.sh
clean:
	rm -rf bin
