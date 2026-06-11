package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"sync"
	"time"
)

const sessionCookie = "munkmail_key"
const loginCSRFCookie = "munkmail_pre"
const sessionTTL = 8 * time.Hour

// Session keeps the IMAP credentials of a logged-in user. Like
// SquirrelMail, we keep the credentials server-side for the lifetime of
// the session and open mail connections on demand.
type Session struct {
	User     string
	Password string
	CSRF     string // per-session anti-CSRF token
	Created  time.Time
	LastSeen time.Time
}

var (
	sessionsMu sync.Mutex
	sessions   = map[string]*Session{}
)

func newSessionID() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func createSession(user, password string) string {
	id := newSessionID()
	now := time.Now()
	sessionsMu.Lock()
	sessions[id] = &Session{
		User: user, Password: password, CSRF: newSessionID(),
		Created: now, LastSeen: now,
	}
	sessionsMu.Unlock()
	return id
}

// validCSRF reports whether the request carries the session's CSRF
// token in the "token" form field. Every state-changing handler must
// check this before acting.
func validCSRF(r *http.Request, s *Session) bool {
	tok := r.FormValue("token")
	return tok != "" && subtle.ConstantTimeCompare([]byte(tok), []byte(s.CSRF)) == 1
}

func getSession(r *http.Request) *Session {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return nil
	}
	sessionsMu.Lock()
	defer sessionsMu.Unlock()
	s := sessions[c.Value]
	if s == nil {
		return nil
	}
	if time.Since(s.LastSeen) > sessionTTL {
		delete(sessions, c.Value)
		return nil
	}
	s.LastSeen = time.Now()
	return s
}

func destroySession(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		sessionsMu.Lock()
		delete(sessions, c.Value)
		sessionsMu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1})
}

// requireAuth wraps a handler and redirects to the login page when no
// valid session is present.
func requireAuth(fn func(http.ResponseWriter, *http.Request, *Session)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s := getSession(r)
		if s == nil {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		fn(w, r, s)
	}
}
