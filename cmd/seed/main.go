// Command seed fills a test mailbox (e.g. GreenMail, see
// docker-compose.yml) with demo messages so the MunkMail UI has
// something to show: plain text mail, an HTML-only mail and a mail
// with an attachment.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"log"
	"time"

	"github.com/emersion/go-message/mail"
	"github.com/emersion/go-smtp"
)

func main() {
	smtpAddr := flag.String("smtp", "localhost:3025", "SMTP server to submit through")
	to := flag.String("to", "munk@example.org", "recipient mailbox to seed")
	count := flag.Int("count", 8, "number of plain text messages")
	flag.Parse()

	for i := 1; i <= *count; i++ {
		raw := plainMessage(
			fmt.Sprintf("Chip Munk <chip%d@example.org>", i),
			*to,
			fmt.Sprintf("Acorn stash report #%d", i),
			fmt.Sprintf("Status update %d:\r\n\r\nThe acorn stash behind the old oak is at %d%% capacity.\r\nWinter readiness is on track.\r\n\r\n-- Chip", i, i*7),
		)
		send(*smtpAddr, fmt.Sprintf("chip%d@example.org", i), *to, raw)
	}

	send(*smtpAddr, "newsletter@example.org", *to, htmlMessage(*to))
	send(*smtpAddr, "jane@example.org", *to, attachmentMessage(*to))

	log.Printf("seeded %d messages to %s via %s", *count+2, *to, *smtpAddr)
}

func send(addr, from, to string, raw []byte) {
	c, err := smtp.Dial(addr)
	if err != nil {
		log.Fatalf("dial %s: %v", addr, err)
	}
	defer c.Close()
	if err := c.SendMail(from, []string{to}, bytes.NewReader(raw)); err != nil {
		log.Fatalf("send: %v", err)
	}
	c.Quit()
}

func plainMessage(from, to, subject, body string) []byte {
	return []byte(fmt.Sprintf(
		"From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMessage-ID: <%d@seed.example.org>\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n%s\r\n",
		from, to, subject, time.Now().Format(time.RFC1123Z), time.Now().UnixNano(), body))
}

func htmlMessage(to string) []byte {
	return []byte(fmt.Sprintf(
		"From: Nut News <newsletter@example.org>\r\nTo: %s\r\nSubject: Nut News Weekly (HTML edition)\r\nDate: %s\r\nMessage-ID: <%d@seed.example.org>\r\nContent-Type: text/html; charset=utf-8\r\n\r\n"+
			"<html><body><h1>Nut News</h1><p>This message has <b>no</b> plain text part.</p><p>MunkMail should convert it to text.</p></body></html>\r\n",
		to, time.Now().Format(time.RFC1123Z), time.Now().UnixNano()))
}

func attachmentMessage(to string) []byte {
	var buf bytes.Buffer
	var h mail.Header
	h.SetDate(time.Now())
	h.SetSubject("Map of the burrow (attachment)")
	h.SetAddressList("From", []*mail.Address{{Name: "Jane Doe", Address: "jane@example.org"}})
	h.SetAddressList("To", []*mail.Address{{Address: to}})

	mw, err := mail.CreateWriter(&buf, h)
	if err != nil {
		log.Fatal(err)
	}
	var th mail.InlineHeader
	th.SetContentType("text/plain", map[string]string{"charset": "utf-8"})
	tw, err := mw.CreateSingleInline(th)
	if err != nil {
		log.Fatal(err)
	}
	io.WriteString(tw, "Hi,\r\n\r\nThe burrow map is attached.\r\n\r\nJane")
	tw.Close()

	var ah mail.AttachmentHeader
	ah.SetContentType("text/csv", nil)
	ah.SetFilename("burrow-map.csv")
	aw, err := mw.CreateAttachment(ah)
	if err != nil {
		log.Fatal(err)
	}
	io.WriteString(aw, "room,depth_cm\r\nentrance,0\r\npantry,80\r\nnest,120\r\n")
	aw.Close()
	mw.Close()
	return buf.Bytes()
}
