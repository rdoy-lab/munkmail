package main

import (
	"bytes"
	"fmt"

	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"
)

// sendMail submits a message to the configured SMTP server,
// authenticating with the webmail user's IMAP credentials.
func sendMail(user, password, from string, recipients []string, raw []byte) error {
	var (
		c   *smtp.Client
		err error
	)
	switch config.SMTPSecurity {
	case "tls":
		c, err = smtp.DialTLS(config.SMTPAddr, nil)
	case "starttls":
		c, err = smtp.DialStartTLS(config.SMTPAddr, nil)
	case "insecure":
		c, err = smtp.Dial(config.SMTPAddr)
	default:
		return fmt.Errorf("unknown SMTP security mode %q", config.SMTPSecurity)
	}
	if err != nil {
		return fmt.Errorf("cannot reach SMTP server: %v", err)
	}
	defer c.Close()

	if ok, _ := c.Extension("AUTH"); ok {
		auth := sasl.NewPlainClient("", user, password)
		if !c.SupportsAuth(sasl.Plain) && c.SupportsAuth(sasl.Login) {
			auth = sasl.NewLoginClient(user, password)
		}
		if err := c.Auth(auth); err != nil {
			return fmt.Errorf("SMTP authentication failed: %v", err)
		}
	}
	if err := c.SendMail(from, recipients, bytes.NewReader(raw)); err != nil {
		return fmt.Errorf("sending failed: %v", err)
	}
	return c.Quit()
}
