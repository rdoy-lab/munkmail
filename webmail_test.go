package main

import (
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
	gosmtp "github.com/emersion/go-smtp"
)

const (
	testUser = "munk"
	testPass = "acorns"
)

// ---- test SMTP backend that records submitted messages ----

type recordedMail struct {
	From string
	To   []string
	Data []byte
}

type testSMTPBackend struct {
	mu   sync.Mutex
	mail []recordedMail
}

type testSMTPSession struct {
	backend *testSMTPBackend
	from    string
	to      []string
}

func (b *testSMTPBackend) NewSession(c *gosmtp.Conn) (gosmtp.Session, error) {
	return &testSMTPSession{backend: b}, nil
}

func (s *testSMTPSession) Reset()        { s.from = ""; s.to = nil }
func (s *testSMTPSession) Logout() error { return nil }
func (s *testSMTPSession) Mail(from string, opts *gosmtp.MailOptions) error {
	s.from = from
	return nil
}
func (s *testSMTPSession) Rcpt(to string, opts *gosmtp.RcptOptions) error {
	s.to = append(s.to, to)
	return nil
}
func (s *testSMTPSession) Data(r io.Reader) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	s.backend.mu.Lock()
	s.backend.mail = append(s.backend.mail, recordedMail{From: s.from, To: s.to, Data: data})
	s.backend.mu.Unlock()
	return nil
}

// ---- environment setup ----

func startTestIMAP(t *testing.T) (string, *imapmemserver.User) {
	t.Helper()
	user := imapmemserver.NewUser(testUser, testPass)
	if err := user.Create("INBOX", nil); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 25; i++ {
		msg := fmt.Sprintf("From: Sender Sciurus <sender%d@example.org>\r\n"+
			"To: munk@example.org\r\n"+
			"Subject: Acorn report %d\r\n"+
			"Date: Mon, 02 Jan 2006 15:04:05 -0700\r\n"+
			"Message-ID: <%d@example.org>\r\n"+
			"Content-Type: text/plain\r\n"+
			"\r\n"+
			"Acorn stash number %d is doing fine.\r\n", i, i, i, i)
		if _, err := user.Append("INBOX", literal{strings.NewReader(msg), int64(len(msg))}, &imap.AppendOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	memServer := imapmemserver.New()
	memServer.AddUser(user)

	srv := imapserver.New(&imapserver.Options{
		NewSession: func(conn *imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return memServer.NewSession(), nil, nil
		},
		InsecureAuth: true,
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return ln.Addr().String(), user
}

type literal struct {
	io.Reader
	size int64
}

func (l literal) Size() int64 { return l.size }

func startTestSMTP(t *testing.T) (string, *testSMTPBackend) {
	t.Helper()
	backend := &testSMTPBackend{}
	srv := gosmtp.NewServer(backend)
	srv.Domain = "example.org"
	srv.AllowInsecureAuth = true
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return ln.Addr().String(), backend
}

func setupEnv(t *testing.T) (*http.Client, string, *testSMTPBackend) {
	client, base, backend, _ := setupEnvUser(t)
	return client, base, backend
}

func setupEnvUser(t *testing.T) (*http.Client, string, *testSMTPBackend, *imapmemserver.User) {
	t.Helper()
	imapAddr, user := startTestIMAP(t)
	smtpAddr, backend := startTestSMTP(t)
	config = Config{
		IMAPAddr: imapAddr, IMAPSecurity: "insecure",
		SMTPAddr: smtpAddr, SMTPSecurity: "insecure",
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
	client := &http.Client{Jar: jar, Timeout: 10 * time.Second}
	return client, "http://" + ln.Addr().String(), backend, user
}

func get(t *testing.T, client *http.Client, u string) string {
	t.Helper()
	resp, err := client.Get(u)
	if err != nil {
		t.Fatalf("GET %s: %v", u, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("GET %s: status %d", u, resp.StatusCode)
	}
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

// extractFormToken pulls the hidden CSRF token field out of a page.
func extractFormToken(t *testing.T, page string) string {
	t.Helper()
	m := regexp.MustCompile(`name="token" value="([0-9a-f]+)"`).FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("no CSRF token field in page: %.300s", page)
	}
	return m[1]
}

// csrfToken fetches the session CSRF token from the message list page.
func csrfToken(t *testing.T, client *http.Client, base string) string {
	t.Helper()
	return extractFormToken(t, get(t, client, base+"/right?folder=INBOX"))
}

func login(t *testing.T, client *http.Client, base string) {
	t.Helper()
	// The login form carries a double-submit CSRF token.
	tok := extractFormToken(t, get(t, client, base+"/login"))
	resp, err := client.PostForm(base+"/login", url.Values{
		"login_username": {testUser}, "secretkey": {testPass}, "token": {tok},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	body := get(t, client, base+"/")
	if !strings.Contains(body, "<frameset") {
		t.Fatalf("expected frameset after login, got: %.200s", body)
	}
}

// ---- tests ----

func TestLoginFailure(t *testing.T) {
	client, base, _ := setupEnv(t)
	tok := extractFormToken(t, get(t, client, base+"/login"))
	resp, err := client.PostForm(base+"/login", url.Values{
		"login_username": {testUser}, "secretkey": {"wrong"}, "token": {tok},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), "Unknown user or password incorrect") {
		t.Fatalf("expected login error, got: %.200s", b)
	}
}

func TestFolderAndMessageList(t *testing.T) {
	client, base, _ := setupEnv(t)
	login(t, client, base)

	left := get(t, client, base+"/left")
	if !strings.Contains(left, "INBOX") {
		t.Fatalf("folder list missing INBOX: %.300s", left)
	}

	right := get(t, client, base+"/right?folder=INBOX")
	if !strings.Contains(right, "Acorn report 25") {
		t.Fatalf("newest message not on page 1:\n%s", right)
	}
	if strings.Contains(right, "Acorn report 5<") {
		t.Fatal("old message unexpectedly on page 1")
	}
	if !strings.Contains(right, "Viewing messages <b>1</b> to <b>20</b> (25 total)") {
		t.Fatalf("pagination summary wrong:\n%s", right)
	}

	right2 := get(t, client, base+"/right?folder=INBOX&page=2")
	if !strings.Contains(right2, "Acorn report 5") || !strings.Contains(right2, "Acorn report 1") {
		t.Fatalf("page 2 missing old messages:\n%s", right2)
	}

	right10 := get(t, client, base+"/right?folder=INBOX&per_page=10")
	if !strings.Contains(right10, "Viewing messages <b>1</b> to <b>10</b> (25 total)") {
		t.Fatalf("per_page=10 summary wrong:\n%s", right10)
	}
}

func TestReadMessage(t *testing.T) {
	client, base, _ := setupEnv(t)
	login(t, client, base)

	body := get(t, client, base+"/read?folder=INBOX&uid=20")
	if !strings.Contains(body, "Acorn report 20") || !strings.Contains(body, "Acorn stash number 20 is doing fine.") {
		t.Fatalf("message body not rendered:\n%s", body)
	}
	if !strings.Contains(body, "Sender Sciurus") {
		t.Fatalf("sender not rendered:\n%s", body)
	}
}

func TestComposeReplyPrefill(t *testing.T) {
	client, base, _ := setupEnv(t)
	login(t, client, base)

	body := get(t, client, base+"/compose?folder=INBOX&uid=20&mode=reply")
	if !strings.Contains(body, "Re: Acorn report 20") {
		t.Fatalf("reply subject not prefilled:\n%s", body)
	}
	if !strings.Contains(body, "&gt; Acorn stash number 20") {
		t.Fatalf("quoted body not prefilled:\n%s", body)
	}
	if !strings.Contains(body, "sender20@example.org") {
		t.Fatalf("reply-to address not prefilled:\n%s", body)
	}
}

func TestSendMailAndSentCopy(t *testing.T) {
	client, base, backend := setupEnv(t)
	login(t, client, base)

	var buf strings.Builder
	mw := multipart.NewWriter(&buf)
	mw.WriteField("token", csrfToken(t, client, base))
	mw.WriteField("folder", "INBOX")
	mw.WriteField("send_to", "friend@example.org")
	mw.WriteField("subject", "Hello from MunkMail")
	mw.WriteField("body", "Stashing nuts for winter.")
	mw.Close()
	resp, err := client.Post(base+"/send", mw.FormDataContentType(), strings.NewReader(buf.String()))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(b), "Message sent.") {
		t.Fatalf("expected sent notice, got:\n%s", b)
	}

	backend.mu.Lock()
	defer backend.mu.Unlock()
	if len(backend.mail) != 1 {
		t.Fatalf("expected 1 SMTP submission, got %d", len(backend.mail))
	}
	m := backend.mail[0]
	if m.From != "munk@example.org" || len(m.To) != 1 || m.To[0] != "friend@example.org" {
		t.Fatalf("bad envelope: %+v", m)
	}
	if !strings.Contains(string(m.Data), "Stashing nuts for winter.") {
		t.Fatalf("body missing from wire message:\n%s", m.Data)
	}

	// The Sent folder must have been created with one message.
	left := get(t, client, base+"/left")
	if !strings.Contains(left, "Sent") {
		t.Fatalf("Sent folder missing from folder list:\n%s", left)
	}
	sent := get(t, client, base+"/right?folder=Sent")
	if !strings.Contains(sent, "Hello from MunkMail") {
		t.Fatalf("sent copy missing:\n%s", sent)
	}
}

func TestDeleteMovesToTrash(t *testing.T) {
	client, base, _ := setupEnv(t)
	login(t, client, base)

	resp, err := client.PostForm(base+"/action", url.Values{
		"folder": {"INBOX"}, "do": {"Delete"}, "msg": {"20"},
		"token": {csrfToken(t, client, base)},
	})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(b), "moved to Trash") {
		t.Fatalf("expected trash notice, got:\n%s", b)
	}
	trash := get(t, client, base+"/right?folder=Trash")
	if !strings.Contains(trash, "Acorn report 20") {
		t.Fatalf("message not in Trash:\n%s", trash)
	}
	inbox := get(t, client, base+"/right?folder=INBOX")
	if strings.Contains(inbox, "Acorn report 20") {
		t.Fatalf("message still in INBOX:\n%s", inbox)
	}
}

func TestUnreadCountsAndFlags(t *testing.T) {
	client, base, _ := setupEnv(t)
	login(t, client, base)

	left := get(t, client, base+"/left")
	if !strings.Contains(left, "(25)") {
		t.Fatalf("expected 25 unseen in folder list:\n%s", left)
	}
	// Reading a message marks it seen.
	get(t, client, base+"/read?folder=INBOX&uid=20")
	left = get(t, client, base+"/left")
	if !strings.Contains(left, "(24)") {
		t.Fatalf("expected 24 unseen after reading:\n%s", left)
	}
	// Mark it unread again via the toolbar action.
	resp, err := client.PostForm(base+"/action", url.Values{
		"folder": {"INBOX"}, "do": {"Unread"}, "msg": {"20"},
		"token": {csrfToken(t, client, base)},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	left = get(t, client, base+"/left")
	if !strings.Contains(left, "(25)") {
		t.Fatalf("expected 25 unseen after marking unread:\n%s", left)
	}
}

func TestFolderManagement(t *testing.T) {
	client, base, _ := setupEnv(t)
	login(t, client, base)

	tok := csrfToken(t, client, base)
	resp, err := client.PostForm(base+"/folders", url.Values{
		"do": {"create"}, "name": {"Projects"}, "token": {tok},
	})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(b), "Folder Projects created.") {
		t.Fatalf("create failed:\n%s", b)
	}

	// Move a message into it.
	resp, err = client.PostForm(base+"/action", url.Values{
		"folder": {"INBOX"}, "do": {"Move"}, "target": {"Projects"}, "msg": {"1"},
		"token": {tok},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	proj := get(t, client, base+"/right?folder=Projects")
	if !strings.Contains(proj, "Acorn report 1") {
		t.Fatalf("message not moved:\n%s", proj)
	}
}

func TestAuthRequired(t *testing.T) {
	_, base, _ := setupEnv(t)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 5 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		}}
	resp, err := client.Get(base + "/right?folder=INBOX")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/login" {
		t.Fatalf("expected redirect to /login, got %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
}

// ---- security regression tests ----

// TestActionCSRFProtection verifies that state-changing requests are
// rejected without the per-session CSRF token and via GET.
func TestActionCSRFProtection(t *testing.T) {
	client, base, _ := setupEnv(t)
	login(t, client, base)

	// GET must be rejected outright (no state changes via links).
	resp, err := client.Get(base + "/action?do=Delete&folder=INBOX&msg=20")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET /action: want 405, got %d", resp.StatusCode)
	}

	// POST without a token must be forbidden.
	for _, form := range []url.Values{
		{"folder": {"INBOX"}, "do": {"Delete"}, "msg": {"20"}},
		{"folder": {"INBOX"}, "do": {"Delete"}, "msg": {"20"}, "token": {"deadbeef"}},
	} {
		resp, err := client.PostForm(base+"/action", form)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("POST /action with form %v: want 403, got %d", form, resp.StatusCode)
		}
	}

	// /folders and /send must be forbidden without a token too.
	resp, err = client.PostForm(base+"/folders", url.Values{"do": {"create"}, "name": {"Evil"}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("POST /folders without token: want 403, got %d", resp.StatusCode)
	}
	var buf strings.Builder
	mw := multipart.NewWriter(&buf)
	mw.WriteField("send_to", "victim@example.org")
	mw.WriteField("body", "spam")
	mw.Close()
	resp, err = client.Post(base+"/send", mw.FormDataContentType(), strings.NewReader(buf.String()))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("POST /send without token: want 403, got %d", resp.StatusCode)
	}

	// The message must still be in the INBOX, untouched.
	inbox := get(t, client, base+"/right?folder=INBOX")
	if !strings.Contains(inbox, "Acorn report 20") {
		t.Fatal("message was deleted despite missing CSRF token")
	}
}

// TestLoginCSRFProtection verifies the double-submit token on /login.
func TestLoginCSRFProtection(t *testing.T) {
	client, base, _ := setupEnv(t)
	resp, err := client.PostForm(base+"/login", url.Values{
		"login_username": {testUser}, "secretkey": {testPass},
	})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(b), "Your login form expired") {
		t.Fatalf("login without CSRF token should be rejected, got: %.300s", b)
	}
}

// TestXSSEscaped delivers a message with hostile headers and body and
// checks that nothing is rendered as markup.
func TestXSSEscaped(t *testing.T) {
	client, base, _, user := setupEnvUser(t)
	login(t, client, base)

	evil := "From: \"<script>alert('from')</script>\" <evil@example.org>\r\n" +
		"To: munk@example.org\r\n" +
		"Subject: <script>alert('subject')</script> & <img src=x onerror=alert(1)>\r\n" +
		"Date: Mon, 02 Jan 2006 15:04:05 -0700\r\n" +
		"Content-Type: text/plain\r\n" +
		"\r\n" +
		"Body with <script>alert('body')</script> and <b>markup</b>.\r\n"
	if _, err := user.Append("INBOX", literal{strings.NewReader(evil), int64(len(evil))}, &imap.AppendOptions{}); err != nil {
		t.Fatal(err)
	}

	for _, page := range []string{
		get(t, client, base+"/right?folder=INBOX"),
		get(t, client, base+"/read?folder=INBOX&uid=26"),
		get(t, client, base+"/compose?folder=INBOX&uid=26&mode=reply"),
	} {
		if strings.Contains(page, "<script>alert(") || strings.Contains(page, "<img src=x") {
			t.Fatalf("unescaped hostile markup in page:\n%.1500s", page)
		}
		if !strings.Contains(page, "&lt;script&gt;") {
			t.Fatalf("expected escaped script tag in page:\n%.1500s", page)
		}
	}
}

// TestSecurityHeaders checks the defense-in-depth headers.
func TestSecurityHeaders(t *testing.T) {
	client, base, _ := setupEnv(t)
	resp, err := client.Get(base + "/login")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	for header, want := range map[string]string{
		"Content-Security-Policy": "default-src 'none'; frame-src 'self'; form-action 'self'; base-uri 'none'",
		"X-Frame-Options":         "SAMEORIGIN",
		"X-Content-Type-Options":  "nosniff",
		"Referrer-Policy":         "no-referrer",
	} {
		if got := resp.Header.Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
}
