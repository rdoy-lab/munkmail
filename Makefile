# MunkMail development helpers.

MAJOR ?= 0
GREENMAIL_IMAP = localhost:3143
GREENMAIL_SMTP = localhost:3025

.PHONY: build test greenmail greenmail-down seed dev test-integration deb

build:
	go build ./...

test:
	go test ./...

# Start the GreenMail test mail server (accounts: munk/acorns, jane/secret).
greenmail:
	docker compose up -d --wait greenmail

greenmail-down:
	docker compose down

# Fill munk's INBOX with demo messages.
seed:
	go run ./cmd/seed -smtp $(GREENMAIL_SMTP) -to munk@example.org

# Run MunkMail against GreenMail on http://localhost:8000
dev:
	go run . -listen :8000 \
		-imap $(GREENMAIL_IMAP) -imap-security insecure \
		-smtp $(GREENMAIL_SMTP) -smtp-security insecure \
		-domain example.org

# Integration tests against a running GreenMail (skipped when it is down).
test-integration: greenmail
	go test -run TestGreenMail -v ./...

# Build a Debian package inside Docker.  Output lands in dist/.
# Set MAJOR on the command line: make deb MAJOR=2
deb: VERSION = $(MAJOR).$(shell git rev-list --count HEAD)+sha.$(shell git rev-parse --short HEAD)
deb:
	@mkdir -p dist
	@echo "Building munkmail $(VERSION)"
	docker build -f Dockerfile.build --build-arg VERSION=$(VERSION) --target artifact -t munkmail-deb . ; \
	CID=$$(docker create munkmail-deb); \
	docker cp $$CID:/out/. dist/ ; \
	docker rm $$CID ; \
	ls -lh dist/*.deb
