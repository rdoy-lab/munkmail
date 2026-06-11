package main

import (
	"bytes"
	"fmt"
	"html"
	"io"
	"net/mail"
	"regexp"
	"strings"
	"time"

	_ "github.com/emersion/go-message/charset"
	gomail "github.com/emersion/go-message/mail"
)

// Attachment describes a non-inline MIME part.
type Attachment struct {
	Index    int // position among attachment parts, used by /download
	Filename string
	MIMEType string
	Size     int
}

// ParsedMessage is the displayable form of a MIME message.
type ParsedMessage struct {
	From        string
	To          string
	Cc          string
	ReplyTo     string
	Date        string
	Subject     string
	Body        string // plain text body
	BodyIsHTML  bool   // true when the body was converted from HTML
	Attachments []Attachment
}

// parseMessage extracts the text body and the attachment list from a
// raw RFC 822 message.
func parseMessage(raw []byte) (*ParsedMessage, error) {
	mr, err := gomail.CreateReader(bytes.NewReader(raw))
	if err != nil && mr == nil {
		return nil, err
	}
	pm := &ParsedMessage{}
	h := mr.Header
	pm.Subject, _ = h.Subject()
	if d, err := h.Date(); err == nil && !d.IsZero() {
		pm.Date = d.Format("Mon, January 2, 2006 3:04 pm")
	}
	pm.From = addrHeader(h, "From")
	pm.To = addrHeader(h, "To")
	pm.Cc = addrHeader(h, "Cc")
	pm.ReplyTo = addrHeader(h, "Reply-To")

	var htmlBody string
	attIndex := 0
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		} else if err != nil {
			break // tolerate malformed trailing parts
		}
		switch ph := p.Header.(type) {
		case *gomail.InlineHeader:
			ct, _, _ := ph.ContentType()
			switch {
			case strings.EqualFold(ct, "text/plain") && pm.Body == "":
				b, _ := io.ReadAll(p.Body)
				pm.Body = string(b)
			case strings.EqualFold(ct, "text/html") && htmlBody == "":
				b, _ := io.ReadAll(p.Body)
				htmlBody = string(b)
			default:
				// treat other inline parts (images etc.) as attachments
				attIndex++
				b, _ := io.ReadAll(p.Body)
				pm.Attachments = append(pm.Attachments, Attachment{
					Index: attIndex, Filename: inlineName(ct, attIndex), MIMEType: ct, Size: len(b),
				})
			}
		case *gomail.AttachmentHeader:
			attIndex++
			name, _ := ph.Filename()
			ct, _, _ := ph.ContentType()
			if name == "" {
				name = inlineName(ct, attIndex)
			}
			b, _ := io.ReadAll(p.Body)
			pm.Attachments = append(pm.Attachments, Attachment{
				Index: attIndex, Filename: name, MIMEType: ct, Size: len(b),
			})
		}
	}
	if pm.Body == "" && htmlBody != "" {
		pm.Body = htmlToText(htmlBody)
		pm.BodyIsHTML = true
	}
	return pm, nil
}

// attachmentBody re-parses the raw message and returns the body of the
// numbered attachment part.
func attachmentBody(raw []byte, index int) (*Attachment, []byte, error) {
	mr, err := gomail.CreateReader(bytes.NewReader(raw))
	if err != nil && mr == nil {
		return nil, nil, err
	}
	attIndex := 0
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		} else if err != nil {
			break
		}
		isAtt := false
		name := ""
		ct := ""
		switch ph := p.Header.(type) {
		case *gomail.InlineHeader:
			c, _, _ := ph.ContentType()
			ct = c
			if !strings.EqualFold(c, "text/plain") && !strings.EqualFold(c, "text/html") {
				isAtt = true
			}
		case *gomail.AttachmentHeader:
			isAtt = true
			name, _ = ph.Filename()
			ct, _, _ = ph.ContentType()
		}
		if !isAtt {
			continue
		}
		attIndex++
		if attIndex != index {
			continue
		}
		if name == "" {
			name = inlineName(ct, attIndex)
		}
		b, err := io.ReadAll(p.Body)
		if err != nil {
			return nil, nil, err
		}
		return &Attachment{Index: index, Filename: name, MIMEType: ct, Size: len(b)}, b, nil
	}
	return nil, nil, fmt.Errorf("attachment %d not found", index)
}

func addrHeader(h gomail.Header, key string) string {
	addrs, err := h.AddressList(key)
	if err != nil || len(addrs) == 0 {
		return h.Get(key)
	}
	var parts []string
	for _, a := range addrs {
		if a.Name != "" {
			parts = append(parts, fmt.Sprintf("%s <%s>", a.Name, a.Address))
		} else {
			parts = append(parts, a.Address)
		}
	}
	return strings.Join(parts, ", ")
}

func inlineName(ct string, n int) string {
	ext := "dat"
	if i := strings.Index(ct, "/"); i >= 0 && i+1 < len(ct) {
		ext = ct[i+1:]
	}
	return fmt.Sprintf("part%d.%s", n, ext)
}

var (
	reScriptStyle = regexp.MustCompile(`(?is)<(script|style)[^>]*>.*?</(script|style)>`)
	reBreaks      = regexp.MustCompile(`(?i)<(br|/p|/div|/tr|/h[1-6])[^>]*>`)
	reTags        = regexp.MustCompile(`<[^>]*>`)
	reBlankLines  = regexp.MustCompile(`\n{3,}`)
)

// htmlToText converts an HTML body to plain text, the way old webmail
// clients did when no text/plain alternative was available.
func htmlToText(s string) string {
	s = reScriptStyle.ReplaceAllString(s, "")
	s = reBreaks.ReplaceAllString(s, "\n")
	s = reTags.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	s = reBlankLines.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

// OutgoingMessage is a message composed in the web UI.
type OutgoingMessage struct {
	From    string
	To      string
	Cc      string
	Bcc     string
	Subject string
	Body    string

	AttachName string
	AttachType string
	AttachData []byte
}

// recipients returns every envelope recipient address.
func (m *OutgoingMessage) recipients() ([]string, error) {
	var out []string
	for _, field := range []string{m.To, m.Cc, m.Bcc} {
		if strings.TrimSpace(field) == "" {
			continue
		}
		addrs, err := mail.ParseAddressList(field)
		if err != nil {
			return nil, fmt.Errorf("bad address list %q: %v", field, err)
		}
		for _, a := range addrs {
			out = append(out, a.Address)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no recipients given")
	}
	return out, nil
}

// build renders the message to wire format (without Bcc header).
func (m *OutgoingMessage) build() ([]byte, error) {
	var h gomail.Header
	h.SetDate(time.Now())
	h.SetSubject(m.Subject)
	h.SetMessageID(fmt.Sprintf("%d.munkmail@%s", time.Now().UnixNano(), config.Domain))
	if err := setAddrHeader(&h, "From", m.From); err != nil {
		return nil, err
	}
	if err := setAddrHeader(&h, "To", m.To); err != nil {
		return nil, err
	}
	if err := setAddrHeader(&h, "Cc", m.Cc); err != nil {
		return nil, err
	}
	h.Set("X-Mailer", "MunkMail (Go)")

	var buf bytes.Buffer
	if len(m.AttachData) == 0 {
		w, err := gomail.CreateSingleInlineWriter(&buf, h)
		if err != nil {
			return nil, err
		}
		if _, err := io.WriteString(w, m.Body); err != nil {
			return nil, err
		}
		if err := w.Close(); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}

	mw, err := gomail.CreateWriter(&buf, h)
	if err != nil {
		return nil, err
	}
	var th gomail.InlineHeader
	th.SetContentType("text/plain", map[string]string{"charset": "utf-8"})
	tw, err := mw.CreateSingleInline(th)
	if err != nil {
		return nil, err
	}
	if _, err := io.WriteString(tw, m.Body); err != nil {
		return nil, err
	}
	tw.Close()

	var ah gomail.AttachmentHeader
	ct := m.AttachType
	if ct == "" {
		ct = "application/octet-stream"
	}
	ah.SetContentType(ct, nil)
	ah.SetFilename(m.AttachName)
	aw, err := mw.CreateAttachment(ah)
	if err != nil {
		return nil, err
	}
	if _, err := aw.Write(m.AttachData); err != nil {
		return nil, err
	}
	aw.Close()
	if err := mw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func setAddrHeader(h *gomail.Header, key, value string) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	addrs, err := mail.ParseAddressList(value)
	if err != nil {
		return fmt.Errorf("bad %s address %q: %v", key, value, err)
	}
	list := make([]*gomail.Address, len(addrs))
	for i, a := range addrs {
		list[i] = &gomail.Address{Name: a.Name, Address: a.Address}
	}
	h.SetAddressList(key, list)
	return nil
}
