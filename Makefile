.PHONY: test test-unit run

test: test-unit

test-unit:
	go test ./...

run:
	go run ./cmd/clawsynapsed --node-id node-alpha
