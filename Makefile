.PHONY: build test vet run web-dev web-build web-lint dev-up dev-down

build:
	go build ./...

vet:
	go vet ./...

test:
	go test ./...

run:
	go run ./cmd/server

web-dev:
	cd web && npm run dev

web-build:
	cd web && npm run build

web-lint:
	cd web && npm run lint

dev-up:
	docker compose up -d

dev-down:
	docker compose down
