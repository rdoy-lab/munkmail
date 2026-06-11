# MunkMail

A SquirrelMail-style webmail client written in Go.

MunkMail is a lightweight IMAP/SMTP frontend that renders plain HTML 4.01 with table layouts, `<font>` tags and framesets so that it works in browsers as old as Internet Explorer 5. Despite the retro UI, it sends modern security headers (CSP, X-Frame-Options, etc.) for defence-in-depth on contemporary browsers.

## Features

- **Retro UI** — Frameset layout reminiscent of classic webmail clients.
- **IMAP/SMTP frontend** — Connects to any standard IMAP and SMTP server.
- **TLS / STARTTLS / insecure** — Configurable security for both IMAP and SMTP.
- **Read, compose, reply, forward** — Full basic mail operations.
- **Folder management** — List and select IMAP mailboxes.
- **Attachment download** — Download MIME parts from messages.
- **Works everywhere** — Pure HTML, no JavaScript required.

## Quick start

```bash
# Build
go build .

# Run against a local mail server
./munkmail \
  -listen :8000 \
  -imap mail.example.com:143 -imap-security starttls \
  -smtp mail.example.com:587 -smtp-security starttls \
  -domain example.com
```

Then open `http://localhost:8000` in your browser.

## Configuration

All settings are passed as command-line flags:

| Flag | Default | Description |
|------|---------|-------------|
| `-listen` | `:8000` | HTTP listen address |
| `-imap` | `localhost:143` | IMAP server `host:port` |
| `-imap-security` | `starttls` | IMAP security: `tls`, `starttls` or `insecure` |
| `-smtp` | `localhost:25` | SMTP server `host:port` |
| `-smtp-security` | `starttls` | SMTP security: `tls`, `starttls` or `insecure` |
| `-domain` | `localhost` | Mail domain appended to bare logins for the `From:` address |
| `-org` | `MunkMail` | Organisation name shown in page titles |

## Development

MunkMail includes a Docker Compose setup with [GreenMail](https://greenmail-mail-test.github.io/greenmail/) for local development and integration testing.

### Start the test mail server

```bash
make greenmail
```

This creates two test accounts:
- `munk` / `acorns` (address: `munk@example.org`)
- `jane` / `secret` (address: `jane@example.org`)

### Seed demo messages

```bash
make seed
```

Sends a handful of test messages to `munk@example.org`.

### Run MunkMail against GreenMail

```bash
make dev
```

Open `http://localhost:8000` and log in as **munk / acorns**.

### Run tests

```bash
# Unit tests
make test

# Integration tests (requires GreenMail running)
make test-integration
```
