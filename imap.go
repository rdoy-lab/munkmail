package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

// dialIMAP connects and logs in to the configured IMAP server.
func dialIMAP(user, password string) (*imapclient.Client, error) {
	var (
		c   *imapclient.Client
		err error
	)
	switch config.IMAPSecurity {
	case "tls":
		c, err = imapclient.DialTLS(config.IMAPAddr, nil)
	case "starttls":
		c, err = imapclient.DialStartTLS(config.IMAPAddr, nil)
	case "insecure":
		c, err = imapclient.DialInsecure(config.IMAPAddr, nil)
	default:
		return nil, fmt.Errorf("unknown IMAP security mode %q", config.IMAPSecurity)
	}
	if err != nil {
		return nil, fmt.Errorf("cannot reach IMAP server: %v", err)
	}
	if err := c.Login(user, password).Wait(); err != nil {
		c.Close()
		return nil, fmt.Errorf("login failed: %v", err)
	}
	return c, nil
}

// Folder is one entry in the folder list.
type Folder struct {
	Name     string
	Display  string // last path component
	Indent   int    // hierarchy depth
	Unseen   uint32
	Total    uint32
	Selected bool
}

// listFolders returns all mailboxes with unseen counts. INBOX is always
// sorted first.
func listFolders(c *imapclient.Client, current string) ([]Folder, error) {
	list, err := c.List("", "*", nil).Collect()
	if err != nil {
		return nil, err
	}
	sort.Slice(list, func(i, j int) bool {
		a, b := list[i].Mailbox, list[j].Mailbox
		if strings.EqualFold(a, "INBOX") {
			return !strings.EqualFold(b, "INBOX")
		}
		if strings.EqualFold(b, "INBOX") {
			return false
		}
		return a < b
	})

	var folders []Folder
	for _, m := range list {
		noSelect := false
		for _, attr := range m.Attrs {
			if attr == imap.MailboxAttrNoSelect {
				noSelect = true
			}
		}
		f := Folder{Name: m.Mailbox, Display: m.Mailbox, Selected: m.Mailbox == current}
		if m.Delim != 0 {
			parts := strings.Split(m.Mailbox, string(m.Delim))
			f.Display = parts[len(parts)-1]
			f.Indent = len(parts) - 1
		}
		if !noSelect {
			status, err := c.Status(m.Mailbox, &imap.StatusOptions{
				NumMessages: true, NumUnseen: true,
			}).Wait()
			if err == nil {
				if status.NumUnseen != nil {
					f.Unseen = *status.NumUnseen
				}
				if status.NumMessages != nil {
					f.Total = *status.NumMessages
				}
			}
		}
		folders = append(folders, f)
	}
	return folders, nil
}

// findSpecialFolder locates a folder by special-use attribute or by a
// list of conventional names. It returns "" when nothing matches.
func findSpecialFolder(c *imapclient.Client, attr imap.MailboxAttr, names ...string) string {
	list, err := c.List("", "*", nil).Collect()
	if err != nil {
		return ""
	}
	for _, m := range list {
		for _, a := range m.Attrs {
			if a == attr {
				return m.Mailbox
			}
		}
	}
	for _, want := range names {
		for _, m := range list {
			if strings.EqualFold(m.Mailbox, want) {
				return m.Mailbox
			}
		}
	}
	return ""
}

// ensureFolder returns the named special folder, creating fallback if
// it does not exist yet.
func ensureFolder(c *imapclient.Client, attr imap.MailboxAttr, fallback string, names ...string) (string, error) {
	if name := findSpecialFolder(c, attr, names...); name != "" {
		return name, nil
	}
	if err := c.Create(fallback, nil).Wait(); err != nil {
		return "", fmt.Errorf("cannot create folder %s: %v", fallback, err)
	}
	return fallback, nil
}

// MsgSummary is one row of the message list.
type MsgSummary struct {
	UID     imap.UID
	Seen    bool
	Flagged bool
	Answer  bool
	Deleted bool
	From    string
	Subject string
	Date    string
	Size    int64
}

// listMessages returns one page of message summaries, newest first.
func listMessages(c *imapclient.Client, folder string, page, perPage int) (msgs []MsgSummary, total uint32, err error) {
	sel, err := c.Select(folder, &imap.SelectOptions{ReadOnly: true}).Wait()
	if err != nil {
		return nil, 0, err
	}
	total = sel.NumMessages
	if total == 0 {
		return nil, 0, nil
	}

	// Newest messages have the highest sequence numbers. Page 1 shows
	// the newest perPage messages.
	hi := int(total) - page*perPage + perPage
	lo := hi - perPage + 1
	if hi < 1 {
		return nil, total, nil
	}
	if lo < 1 {
		lo = 1
	}
	var set imap.SeqSet
	set.AddRange(uint32(lo), uint32(hi))

	bufs, err := c.Fetch(set, &imap.FetchOptions{
		UID: true, Flags: true, Envelope: true, RFC822Size: true,
	}).Collect()
	if err != nil {
		return nil, total, err
	}
	sort.Slice(bufs, func(i, j int) bool { return bufs[i].SeqNum > bufs[j].SeqNum })

	for _, b := range bufs {
		m := MsgSummary{UID: b.UID, Size: b.RFC822Size}
		for _, f := range b.Flags {
			switch f {
			case imap.FlagSeen:
				m.Seen = true
			case imap.FlagFlagged:
				m.Flagged = true
			case imap.FlagAnswered:
				m.Answer = true
			case imap.FlagDeleted:
				m.Deleted = true
			}
		}
		if env := b.Envelope; env != nil {
			m.Subject = env.Subject
			m.From = formatAddrList(env.From)
			if !env.Date.IsZero() {
				m.Date = env.Date.Format("Jan 2, 2006 3:04 pm")
			}
		}
		if m.Subject == "" {
			m.Subject = "(no subject)"
		}
		if m.From == "" {
			m.From = "Unknown sender"
		}
		msgs = append(msgs, m)
	}
	return msgs, total, nil
}

func formatAddrList(addrs []imap.Address) string {
	var parts []string
	for _, a := range addrs {
		if a.Name != "" {
			parts = append(parts, a.Name)
		} else {
			parts = append(parts, a.Addr())
		}
	}
	return strings.Join(parts, ", ")
}

func formatAddrListFull(addrs []imap.Address) string {
	var parts []string
	for _, a := range addrs {
		if a.Name != "" {
			parts = append(parts, fmt.Sprintf("%s <%s>", a.Name, a.Addr()))
		} else {
			parts = append(parts, a.Addr())
		}
	}
	return strings.Join(parts, ", ")
}

// fetchRawMessage downloads the full RFC 822 message for the given UID.
func fetchRawMessage(c *imapclient.Client, folder string, uid imap.UID, markSeen bool) ([]byte, *imapclient.FetchMessageBuffer, error) {
	if _, err := c.Select(folder, nil).Wait(); err != nil {
		return nil, nil, err
	}
	section := &imap.FetchItemBodySection{Peek: !markSeen}
	bufs, err := c.Fetch(imap.UIDSetNum(uid), &imap.FetchOptions{
		UID: true, Flags: true, Envelope: true, RFC822Size: true,
		BodySection: []*imap.FetchItemBodySection{section},
	}).Collect()
	if err != nil {
		return nil, nil, err
	}
	if len(bufs) == 0 {
		return nil, nil, fmt.Errorf("message not found")
	}
	return bufs[0].FindBodySection(section), bufs[0], nil
}

// moveMessages moves UIDs to another folder, falling back to
// COPY + STORE \Deleted + EXPUNGE when MOVE is unsupported.
func moveMessages(c *imapclient.Client, fromFolder string, uids []imap.UID, toFolder string) error {
	if _, err := c.Select(fromFolder, nil).Wait(); err != nil {
		return err
	}
	set := imap.UIDSetNum(uids...)
	if c.Caps().Has(imap.CapMove) {
		_, err := c.Move(set, toFolder).Wait()
		return err
	}
	if _, err := c.Copy(set, toFolder).Wait(); err != nil {
		return err
	}
	if err := c.Store(set, &imap.StoreFlags{
		Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{imap.FlagDeleted},
	}, nil).Close(); err != nil {
		return err
	}
	return c.Expunge().Close()
}

// storeFlag adds or removes a flag on the given UIDs.
func storeFlag(c *imapclient.Client, folder string, uids []imap.UID, flag imap.Flag, add bool) error {
	if _, err := c.Select(folder, nil).Wait(); err != nil {
		return err
	}
	op := imap.StoreFlagsAdd
	if !add {
		op = imap.StoreFlagsDel
	}
	return c.Store(imap.UIDSetNum(uids...), &imap.StoreFlags{
		Op: op, Silent: true, Flags: []imap.Flag{flag},
	}, nil).Close()
}

// appendMessage stores a raw message in the given folder (e.g. Sent).
func appendMessage(c *imapclient.Client, folder string, raw []byte, flags []imap.Flag) error {
	cmd := c.Append(folder, int64(len(raw)), &imap.AppendOptions{Flags: flags})
	if _, err := cmd.Write(raw); err != nil {
		cmd.Close()
		return err
	}
	if err := cmd.Close(); err != nil {
		return err
	}
	_, err := cmd.Wait()
	return err
}
