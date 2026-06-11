package main

import (
	"strings"
	"testing"
)

// TestNoHeaderInjection verifies that hostile CRLF sequences in
// compose form fields cannot inject extra mail headers into the wire
// format (go-message must encode them away).
func TestNoHeaderInjection(t *testing.T) {
	config.Domain = "example.org"
	msg := &OutgoingMessage{
		From:    "munk@example.org",
		To:      "friend@example.org",
		Subject: "Hello\r\nBcc: evil@example.org\r\nX-Injected: yes",
		Body:    "harmless body",
	}
	raw, err := msg.build()
	if err != nil {
		t.Fatal(err)
	}
	headers := string(raw[:strings.Index(string(raw), "\r\n\r\n")])
	for _, line := range strings.Split(headers, "\r\n") {
		if strings.HasPrefix(line, "Bcc:") || strings.HasPrefix(line, "X-Injected:") {
			t.Fatalf("injected header line %q in:\n%s", line, headers)
		}
	}
}

// TestAddressFieldRejectsCRLF verifies that newlines in address fields
// are rejected at parse time.
func TestAddressFieldRejectsCRLF(t *testing.T) {
	msg := &OutgoingMessage{
		From: "munk@example.org",
		To:   "friend@example.org\r\nBcc: evil@example.org",
		Body: "x",
	}
	if _, err := msg.recipients(); err == nil {
		t.Fatal("expected address list with CRLF to be rejected")
	}
	if _, err := msg.build(); err == nil {
		t.Fatal("expected build with CRLF address to fail")
	}
}
