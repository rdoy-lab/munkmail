package main

import (
	"crypto/subtle"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
)

const perPage = 15

func handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	s := getSession(r)
	if s == nil {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	render(w, "index.html", map[string]string{"Org": config.OrgName, "User": s.User})
}

// renderLoginForm shows the login page with a fresh double-submit CSRF
// token: the token is stored in a cookie and embedded in the form, and
// both must match on POST. This prevents login CSRF.
func renderLoginForm(w http.ResponseWriter, user, errMsg string) {
	tok := newSessionID()
	http.SetCookie(w, &http.Cookie{
		Name: loginCSRFCookie, Value: tok, Path: "/",
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
	render(w, "login.html", map[string]string{
		"Org": config.OrgName, "User": user, "Error": errMsg, "Token": tok,
	})
}

func handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		renderLoginForm(w, "", "")
		return
	}
	c2, err := r.Cookie(loginCSRFCookie)
	tok := r.FormValue("token")
	if err != nil || tok == "" || subtle.ConstantTimeCompare([]byte(tok), []byte(c2.Value)) != 1 {
		renderLoginForm(w, "", "Your login form expired. Please try again.")
		return
	}
	user := strings.TrimSpace(r.FormValue("login_username"))
	pass := r.FormValue("secretkey")
	if user == "" || pass == "" {
		renderLoginForm(w, user, "You must enter a name and a password.")
		return
	}
	c, err := dialIMAP(user, pass)
	if err != nil {
		log.Printf("login %s: %v", user, err)
		renderLoginForm(w, user, "Unknown user or password incorrect.")
		return
	}
	c.Logout().Wait()
	c.Close()

	id := createSession(user, pass)
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: id, Path: "/",
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
	http.SetCookie(w, &http.Cookie{Name: loginCSRFCookie, Value: "", Path: "/", MaxAge: -1})
	http.Redirect(w, r, "/", http.StatusFound)
}

func handleLogout(w http.ResponseWriter, r *http.Request) {
	destroySession(w, r)
	http.Redirect(w, r, "/login", http.StatusFound)
}

func handleLeft(w http.ResponseWriter, r *http.Request, s *Session) {
	c, err := dialIMAP(s.User, s.Password)
	if err != nil {
		renderError(w, "/left", err.Error())
		return
	}
	defer c.Close()
	folders, err := listFolders(c, r.FormValue("folder"))
	if err != nil {
		renderError(w, "/left", err.Error())
		return
	}
	render(w, "left.html", map[string]interface{}{
		"Org":     config.OrgName,
		"Folders": folders,
		"Time":    time.Now().Format("Mon, 3:04 pm"),
	})
}

func handleRight(w http.ResponseWriter, r *http.Request, s *Session) {
	folder := r.FormValue("folder")
	if folder == "" {
		folder = "INBOX"
	}
	page, _ := strconv.Atoi(r.FormValue("page"))
	if page < 1 {
		page = 1
	}
	backURL := "/right?folder=" + url.QueryEscape(folder)

	c, err := dialIMAP(s.User, s.Password)
	if err != nil {
		renderError(w, backURL, err.Error())
		return
	}
	defer c.Close()

	msgs, total, err := listMessages(c, folder, page, perPage)
	if err != nil {
		renderError(w, backURL, fmt.Sprintf("Could not open folder %s: %v", folder, err))
		return
	}
	folders, _ := listFolders(c, folder)

	first := (page-1)*perPage + 1
	last := first + len(msgs) - 1
	if len(msgs) == 0 {
		first = 0
		last = 0
	}
	render(w, "right.html", map[string]interface{}{
		"Org":      config.OrgName,
		"Folder":   folder,
		"Page":     page,
		"Msgs":     msgs,
		"Total":    total,
		"Folders":  folders,
		"FirstNum": first,
		"LastNum":  last,
		"HasPrev":  page > 1,
		"HasNext":  uint32(page*perPage) < total,
		"Note":     r.FormValue("note"),
		"Token":    s.CSRF,
	})
}

func handleRead(w http.ResponseWriter, r *http.Request, s *Session) {
	folder := r.FormValue("folder")
	page := r.FormValue("page")
	uid, err := parseUID(r.FormValue("uid"))
	backURL := "/right?folder=" + url.QueryEscape(folder) + "&page=" + url.QueryEscape(page)
	if folder == "" || err != nil {
		renderError(w, backURL, "Bad message reference.")
		return
	}

	c, err := dialIMAP(s.User, s.Password)
	if err != nil {
		renderError(w, backURL, err.Error())
		return
	}
	defer c.Close()

	raw, _, err := fetchRawMessage(c, folder, uid, true)
	if err != nil {
		renderError(w, backURL, fmt.Sprintf("Could not fetch message: %v", err))
		return
	}
	pm, err := parseMessage(raw)
	if err != nil {
		renderError(w, backURL, fmt.Sprintf("Could not parse message: %v", err))
		return
	}
	if pm.Subject == "" {
		pm.Subject = "(no subject)"
	}
	render(w, "read.html", map[string]interface{}{
		"Org":    config.OrgName,
		"Folder": folder,
		"Page":   pageOr1(page),
		"UID":    uint32(uid),
		"Msg":    pm,
		"Token":  s.CSRF,
	})
}

func handleCompose(w http.ResponseWriter, r *http.Request, s *Session) {
	folder := r.FormValue("folder")
	if folder == "" {
		folder = "INBOX"
	}
	data := map[string]interface{}{
		"Org":     config.OrgName,
		"Folder":  folder,
		"From":    fromAddress(s.User),
		"To":      r.FormValue("to"),
		"Cc":      "",
		"Subject": "",
		"Body":    "",
		"Token":   s.CSRF,
	}

	mode := r.FormValue("mode")
	if uidStr := r.FormValue("uid"); uidStr != "" && mode != "" {
		uid, err := parseUID(uidStr)
		if err == nil {
			if err := prefillCompose(s, folder, uid, mode, data); err != nil {
				data["Error"] = err.Error()
			}
		}
	}
	render(w, "compose.html", data)
}

// prefillCompose fills the compose form for reply / replyall / forward.
func prefillCompose(s *Session, folder string, uid imap.UID, mode string, data map[string]interface{}) error {
	c, err := dialIMAP(s.User, s.Password)
	if err != nil {
		return err
	}
	defer c.Close()
	raw, buf, err := fetchRawMessage(c, folder, uid, false)
	if err != nil {
		return err
	}
	pm, err := parseMessage(raw)
	if err != nil {
		return err
	}
	env := buf.Envelope

	quoted := "> " + strings.ReplaceAll(strings.ReplaceAll(pm.Body, "\r\n", "\n"), "\n", "\n> ")
	switch mode {
	case "reply", "replyall":
		to := pm.ReplyTo
		if to == "" {
			to = pm.From
		}
		data["To"] = to
		if mode == "replyall" && env != nil {
			var cc []string
			if extra := formatAddrListFull(env.To); extra != "" {
				cc = append(cc, extra)
			}
			if extra := formatAddrListFull(env.Cc); extra != "" {
				cc = append(cc, extra)
			}
			data["Cc"] = strings.Join(cc, ", ")
		}
		subj := pm.Subject
		if !strings.HasPrefix(strings.ToLower(subj), "re:") {
			subj = "Re: " + subj
		}
		data["Subject"] = subj
		data["Body"] = fmt.Sprintf("\n\n%s wrote:\n%s", pm.From, quoted)
	case "forward":
		subj := pm.Subject
		if !strings.HasPrefix(strings.ToLower(subj), "fwd:") {
			subj = "Fwd: " + subj
		}
		data["Subject"] = subj
		data["Body"] = fmt.Sprintf(
			"\n\n-------- Original Message --------\nSubject: %s\nFrom: %s\nDate: %s\nTo: %s\n\n%s",
			pm.Subject, pm.From, pm.Date, pm.To, pm.Body)
	}
	return nil
}

func handleSend(w http.ResponseWriter, r *http.Request, s *Session) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/compose", http.StatusFound)
		return
	}
	if err := r.ParseMultipartForm(16 << 20); err != nil {
		renderError(w, "/compose", "Could not read form: "+err.Error())
		return
	}
	if !validCSRF(r, s) {
		http.Error(w, "Forbidden: missing or invalid CSRF token", http.StatusForbidden)
		return
	}
	folder := r.FormValue("folder")
	if folder == "" {
		folder = "INBOX"
	}
	msg := &OutgoingMessage{
		From:    fromAddress(s.User),
		To:      r.FormValue("send_to"),
		Cc:      r.FormValue("send_to_cc"),
		Bcc:     r.FormValue("send_to_bcc"),
		Subject: r.FormValue("subject"),
		Body:    r.FormValue("body"),
	}
	if f, fh, err := r.FormFile("attachment"); err == nil {
		data, err := io.ReadAll(f)
		f.Close()
		if err == nil && len(data) > 0 {
			msg.AttachName = fh.Filename
			msg.AttachType = fh.Header.Get("Content-Type")
			msg.AttachData = data
		}
	}

	failBack := func(errMsg string) {
		render(w, "compose.html", map[string]interface{}{
			"Org": config.OrgName, "Folder": folder, "From": msg.From,
			"To": msg.To, "Cc": msg.Cc, "Subject": msg.Subject, "Body": msg.Body,
			"Error": errMsg, "Token": s.CSRF,
		})
	}

	rcpts, err := msg.recipients()
	if err != nil {
		failBack(err.Error())
		return
	}
	raw, err := msg.build()
	if err != nil {
		failBack(err.Error())
		return
	}
	if err := sendMail(s.User, s.Password, msg.From, rcpts, raw); err != nil {
		failBack(err.Error())
		return
	}

	// Save a copy to the Sent folder; failures here are not fatal.
	note := "Message sent."
	if c, err := dialIMAP(s.User, s.Password); err == nil {
		if sent, err := ensureFolder(c, imap.MailboxAttrSent, "Sent", "Sent", "Sent Items", "Sent Messages"); err == nil {
			if err := appendMessage(c, sent, raw, []imap.Flag{imap.FlagSeen}); err != nil {
				note = "Message sent (could not save to Sent folder)."
			}
		}
		c.Logout().Wait()
		c.Close()
	}
	http.Redirect(w, r, "/right?folder="+url.QueryEscape(folder)+"&note="+url.QueryEscape(note), http.StatusFound)
}

func handleAction(w http.ResponseWriter, r *http.Request, s *Session) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.ParseForm()
	if !validCSRF(r, s) {
		http.Error(w, "Forbidden: missing or invalid CSRF token", http.StatusForbidden)
		return
	}
	folder := r.FormValue("folder")
	page := pageOr1(r.FormValue("page"))
	backURL := fmt.Sprintf("/right?folder=%s&page=%d", url.QueryEscape(folder), page)

	var uids []imap.UID
	for _, v := range r.Form["msg"] {
		if uid, err := parseUID(v); err == nil {
			uids = append(uids, uid)
		}
	}
	if folder == "" || len(uids) == 0 {
		http.Redirect(w, r, backURL+"&note="+url.QueryEscape("No messages selected."), http.StatusFound)
		return
	}

	c, err := dialIMAP(s.User, s.Password)
	if err != nil {
		renderError(w, backURL, err.Error())
		return
	}
	defer c.Close()

	var note string
	switch r.FormValue("do") {
	case "Delete":
		trash, terr := ensureFolder(c, imap.MailboxAttrTrash, "Trash", "Trash", "Deleted Items", "Deleted Messages")
		if terr == nil && !strings.EqualFold(folder, trash) {
			err = moveMessages(c, folder, uids, trash)
			note = fmt.Sprintf("%d message(s) moved to %s.", len(uids), trash)
		} else {
			// already in Trash: delete permanently
			if err = storeFlag(c, folder, uids, imap.FlagDeleted, true); err == nil {
				err = c.Expunge().Close()
			}
			note = fmt.Sprintf("%d message(s) deleted.", len(uids))
		}
	case "Move":
		target := r.FormValue("target")
		if target == "" || target == folder {
			note = "Choose a different target folder."
		} else {
			err = moveMessages(c, folder, uids, target)
			note = fmt.Sprintf("%d message(s) moved to %s.", len(uids), target)
		}
	case "Read":
		err = storeFlag(c, folder, uids, imap.FlagSeen, true)
		note = "Messages marked as read."
	case "Unread":
		err = storeFlag(c, folder, uids, imap.FlagSeen, false)
		note = "Messages marked as unread."
	default:
		note = "Unknown action."
	}
	if err != nil {
		renderError(w, backURL, err.Error())
		return
	}
	http.Redirect(w, r, backURL+"&note="+url.QueryEscape(note), http.StatusFound)
}

func handleDownload(w http.ResponseWriter, r *http.Request, s *Session) {
	folder := r.FormValue("folder")
	uid, err := parseUID(r.FormValue("uid"))
	part, _ := strconv.Atoi(r.FormValue("part"))
	backURL := "/right?folder=" + url.QueryEscape(folder)
	if folder == "" || err != nil || part < 1 {
		renderError(w, backURL, "Bad attachment reference.")
		return
	}
	c, err := dialIMAP(s.User, s.Password)
	if err != nil {
		renderError(w, backURL, err.Error())
		return
	}
	defer c.Close()
	raw, _, err := fetchRawMessage(c, folder, uid, false)
	if err != nil {
		renderError(w, backURL, err.Error())
		return
	}
	att, body, err := attachmentBody(raw, part)
	if err != nil {
		renderError(w, backURL, err.Error())
		return
	}
	// application/octet-stream + attachment disposition keeps IE5 from
	// trying to render the part inline.
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", att.Filename))
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.Write(body)
}

func handleRaw(w http.ResponseWriter, r *http.Request, s *Session) {
	folder := r.FormValue("folder")
	page := r.FormValue("page")
	uid, err := parseUID(r.FormValue("uid"))
	backURL := "/right?folder=" + url.QueryEscape(folder) + "&page=" + url.QueryEscape(page)
	if folder == "" || err != nil {
		renderError(w, backURL, "Bad message reference.")
		return
	}

	c, err := dialIMAP(s.User, s.Password)
	if err != nil {
		renderError(w, backURL, err.Error())
		return
	}
	defer c.Close()

	raw, _, err := fetchRawMessage(c, folder, uid, false)
	if err != nil {
		renderError(w, backURL, fmt.Sprintf("Could not fetch message: %v", err))
		return
	}

	render(w, "raw.html", map[string]interface{}{
		"Org":     config.OrgName,
		"Folder":  folder,
		"Page":    pageOr1(page),
		"UID":     uint32(uid),
		"RawBody": string(raw),
	})
}

func handleFolders(w http.ResponseWriter, r *http.Request, s *Session) {
	c, err := dialIMAP(s.User, s.Password)
	if err != nil {
		renderError(w, "/folders", err.Error())
		return
	}
	defer c.Close()

	note := ""
	if r.Method == http.MethodPost {
		if !validCSRF(r, s) {
			http.Error(w, "Forbidden: missing or invalid CSRF token", http.StatusForbidden)
			return
		}
		name := strings.TrimSpace(r.FormValue("name"))
		switch r.FormValue("do") {
		case "create":
			if name == "" {
				note = "Folder name required."
			} else if err := c.Create(name, nil).Wait(); err != nil {
				note = "Create failed: " + err.Error()
			} else {
				note = "Folder " + name + " created."
			}
		case "delete":
			if strings.EqualFold(name, "INBOX") {
				note = "Cannot delete INBOX."
			} else if err := c.Delete(name).Wait(); err != nil {
				note = "Delete failed: " + err.Error()
			} else {
				note = "Folder " + name + " deleted."
			}
		case "rename":
			newName := strings.TrimSpace(r.FormValue("newname"))
			if name == "" || newName == "" {
				note = "Both names are required."
			} else if err := c.Rename(name, newName, nil).Wait(); err != nil {
				note = "Rename failed: " + err.Error()
			} else {
				note = "Folder renamed to " + newName + "."
			}
		}
	}

	folders, err := listFolders(c, "")
	if err != nil {
		renderError(w, "/right?folder=INBOX", err.Error())
		return
	}
	render(w, "folders.html", map[string]interface{}{
		"Org":     config.OrgName,
		"Folders": folders,
		"Note":    note,
		"Token":   s.CSRF,
	})
}

func parseUID(s string) (imap.UID, error) {
	n, err := strconv.ParseUint(s, 10, 32)
	if err != nil || n == 0 {
		return 0, fmt.Errorf("bad uid %q", s)
	}
	return imap.UID(n), nil
}

func pageOr1(s string) int {
	n, _ := strconv.Atoi(s)
	if n < 1 {
		return 1
	}
	return n
}

func fromAddress(user string) string {
	if strings.Contains(user, "@") {
		return user
	}
	return user + "@" + config.Domain
}
