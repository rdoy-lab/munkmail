package main

import (
	"fmt"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"time"
)

// GreenMail addresses used by docker-compose.yml.
const (
	greenmailIMAP = "localhost:3143"
	greenmailSMTP = "localhost:3025"
	greenmailUser = "munk"
	greenmailPass = "acorns"
)

// setupGreenMailEnv points the app at a running GreenMail instance and
// returns an HTTP client for the web UI. The test is skipped when
// GreenMail is not reachable (start it with `make greenmail`).
func setupGreenMailEnv(t *testing.T) (*http.Client, string) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", greenmailIMAP, time.Second)
	if err != nil {
		t.Skipf("GreenMail not running on %s (start with `make greenmail`): %v", greenmailIMAP, err)
	}
	conn.Close()

	config = Config{
		IMAPAddr: greenmailIMAP, IMAPSecurity: "insecure",
		SMTPAddr: greenmailSMTP, SMTPSecurity: "insecure",
		Domain: "example.org", OrgName: "MunkMail",
	}
	httpSrv := &http.Server{Handler: newMux()}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go httpSrv.Serve(ln)
	t.Cleanup(func() { httpSrv.Close() })

	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, Timeout: 15 * time.Second}, "http://" + ln.Addr().String()
}

func greenmailLogin(t *testing.T, client *http.Client, base string) {
	t.Helper()
	tok := extractFormToken(t, get(t, client, base+"/login"))
	resp, err := client.PostForm(base+"/login", url.Values{
		"login_username": {greenmailUser}, "secretkey": {greenmailPass}, "token": {tok},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	body := get(t, client, base+"/")
	if !strings.Contains(body, "<frameset") {
		t.Fatalf("login against GreenMail failed: %.300s", body)
	}
}

// TestGreenMailRoundTrip exercises the full stack against a real
// GreenMail server: deliver a message over SMTP, find it in the INBOX
// via the web UI, read it, reply to it, and check the Sent copy.
func TestGreenMailRoundTrip(t *testing.T) {
	client, base := setupGreenMailEnv(t)
	greenmailLogin(t, client, base)

	// Deliver a message with a unique subject straight over SMTP,
	// the same way the seed command does.
	marker := fmt.Sprintf("integration-%d", time.Now().UnixNano())
	raw := []byte(fmt.Sprintf(
		"From: Tester <tester@example.org>\r\nTo: munk@example.org\r\nSubject: %s\r\nDate: %s\r\nContent-Type: text/plain\r\n\r\nGreenMail round trip body.\r\n",
		marker, time.Now().Format(time.RFC1123Z)))
	if err := sendMail(greenmailUser, greenmailPass, "tester@example.org", []string{"munk@example.org"}, raw); err != nil {
		t.Fatalf("SMTP delivery to GreenMail failed: %v", err)
	}

	// The new message must appear at the top of the INBOX.
	inbox := get(t, client, base+"/right?folder=INBOX")
	if !strings.Contains(inbox, marker) {
		t.Fatalf("delivered message %s not in INBOX:\n%.1000s", marker, inbox)
	}

	// Extract its UID from the read link and open it.
	uid := extractUID(t, inbox, marker)
	msg := get(t, client, base+"/read?folder=INBOX&uid="+uid)
	if !strings.Contains(msg, "GreenMail round trip body.") {
		t.Fatalf("message body not rendered:\n%.1000s", msg)
	}

	// Reply prefill must quote the body.
	reply := get(t, client, base+"/compose?folder=INBOX&uid="+uid+"&mode=reply")
	if !strings.Contains(reply, "Re: "+marker) || !strings.Contains(reply, "&gt; GreenMail round trip body.") {
		t.Fatalf("reply prefill wrong:\n%.1000s", reply)
	}

	// Send a reply through the UI and verify the Sent copy.
	tok := csrfToken(t, client, base)
	var buf strings.Builder
	mw := multipart.NewWriter(&buf)
	mw.WriteField("token", tok)
	mw.WriteField("folder", "INBOX")
	mw.WriteField("send_to", "jane@example.org")
	mw.WriteField("subject", "Re: "+marker)
	mw.WriteField("body", "Reply sent through MunkMail.")
	mw.Close()
	resp, err := client.Post(base+"/send", mw.FormDataContentType(), strings.NewReader(buf.String()))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	sent := get(t, client, base+"/right?folder=Sent")
	if !strings.Contains(sent, "Re: "+marker) {
		t.Fatalf("Sent copy missing:\n%.1000s", sent)
	}

	// Delete the original; it must land in Trash.
	resp, err = client.PostForm(base+"/action", url.Values{
		"folder": {"INBOX"}, "do": {"Delete"}, "msg": {uid}, "token": {tok},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	trash := get(t, client, base+"/right?folder=Trash")
	if !strings.Contains(trash, marker) {
		t.Fatalf("deleted message not in Trash:\n%.1000s", trash)
	}
}

// extractUID finds the uid= parameter of the read link for the row
// containing the given subject.
func extractUID(t *testing.T, page, subject string) string {
	t.Helper()
	idx := strings.Index(page, ">"+subject+"<")
	if idx < 0 {
		t.Fatalf("subject %q not found in page", subject)
	}
	// The link looks like: /read?folder=INBOX&uid=NN&page=1
	linkStart := strings.LastIndex(page[:idx], "uid=")
	if linkStart < 0 {
		t.Fatalf("no uid link before subject %q", subject)
	}
	rest := page[linkStart+4:]
	end := strings.IndexAny(rest, "&\"")
	if end < 0 {
		t.Fatalf("malformed uid link")
	}
	return rest[:end]
}
