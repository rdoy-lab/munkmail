// MunkMail: a SquirrelMail-style webmail client written in Go.
//
// It is an IMAP/SMTP frontend that renders plain HTML 4.01 with table
// layouts, font tags and framesets so that it works in browsers as old
// as Internet Explorer 5.
package main

import (
	"flag"
	"log"
	"net/http"
)

// Config holds the server configuration.
type Config struct {
	Listen       string
	IMAPAddr     string
	IMAPSecurity string // tls, starttls, insecure
	SMTPAddr     string
	SMTPSecurity string // tls, starttls, insecure
	Domain       string // default domain appended to bare logins for the From address
	OrgName      string
}

var config Config

func main() {
	flag.StringVar(&config.Listen, "listen", ":8000", "HTTP listen address")
	flag.StringVar(&config.IMAPAddr, "imap", "localhost:143", "IMAP server address (host:port)")
	flag.StringVar(&config.IMAPSecurity, "imap-security", "starttls", "IMAP security: tls, starttls or insecure")
	flag.StringVar(&config.SMTPAddr, "smtp", "localhost:25", "SMTP server address (host:port)")
	flag.StringVar(&config.SMTPSecurity, "smtp-security", "starttls", "SMTP security: tls, starttls or insecure")
	flag.StringVar(&config.Domain, "domain", "localhost", "mail domain used for the From address when the login has no @")
	flag.StringVar(&config.OrgName, "org", "MunkMail", "organization name shown in page titles")
	flag.Parse()

	log.Printf("MunkMail listening on %s (IMAP %s/%s, SMTP %s/%s)",
		config.Listen, config.IMAPAddr, config.IMAPSecurity, config.SMTPAddr, config.SMTPSecurity)
	log.Fatal(http.ListenAndServe(config.Listen, newMux()))
}

// newMux builds the HTTP routing table wrapped with security headers.
func newMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", handleRoot)
	mux.HandleFunc("/login", handleLogin)
	mux.HandleFunc("/logout", handleLogout)
	mux.HandleFunc("/left", requireAuth(handleLeft))
	mux.HandleFunc("/right", requireAuth(handleRight))
	mux.HandleFunc("/read", requireAuth(handleRead))
	mux.HandleFunc("/compose", requireAuth(handleCompose))
	mux.HandleFunc("/send", requireAuth(handleSend))
	mux.HandleFunc("/action", requireAuth(handleAction))
	mux.HandleFunc("/download", requireAuth(handleDownload))
	mux.HandleFunc("/raw", requireAuth(handleRaw))
	mux.HandleFunc("/folders", requireAuth(handleFolders))
	return securityHeaders(mux)
}

// securityHeaders adds XSS/clickjacking defense-in-depth headers for
// modern browsers. IE5 ignores all of these, so the app still renders
// there; the app serves no scripts or styles, so the strict CSP costs
// nothing.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy",
			"default-src 'none'; frame-src 'self'; form-action 'self'; base-uri 'none'")
		h.Set("X-Frame-Options", "SAMEORIGIN")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}
