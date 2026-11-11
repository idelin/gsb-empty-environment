package imapsync

import (
	"fmt"
	"io"
	"strings"
)

// Client is a minimal IMAP4rev1 client speaking over an io.ReadWriter.
type Client struct {
	p   *parser
	w   io.Writer
	tag int

	// Exists tracks the mailbox message count reported by untagged
	// EXISTS responses; Expunged counts untagged EXPUNGE responses.
	Exists   int
	Expunged int
}

// NewClient wraps an established connection (greeting not yet read).
func NewClient(rw io.ReadWriter) *Client {
	return &Client{p: newParser(rw), w: rw}
}

// Greeting reads and validates the server greeting.
func (c *Client) Greeting() error {
	resp, err := c.p.readResponse()
	if err != nil {
		return fmt.Errorf("imapsync: reading greeting: %w", err)
	}
	if resp.Tag != "*" || (resp.Type != "OK" && resp.Type != "PREAUTH") {
		return fmt.Errorf("imapsync: unexpected greeting: %s %s", resp.Tag, resp.Type)
	}
	return nil
}

func (c *Client) nextTag() string {
	c.tag++
	return fmt.Sprintf("A%04d", c.tag)
}

// CmdError reports a NO/BAD tagged completion for a command, as opposed
// to a transport failure.
type CmdError struct {
	Cmd    string
	Status string
	Text   string
}

func (e *CmdError) Error() string {
	return fmt.Sprintf("imapsync: command %q failed: %s %s", e.Cmd, e.Status, e.Text)
}

// Do sends one command (without tag/CRLF) and returns the untagged
// responses collected while waiting for the tagged completion.
func (c *Client) Do(cmd string) ([]*Response, error) {
	tag := c.nextTag()
	if _, err := fmt.Fprintf(c.w, "%s %s\r\n", tag, cmd); err != nil {
		return nil, err
	}
	var untagged []*Response
	for {
		resp, err := c.p.readResponse()
		if err != nil {
			return untagged, err
		}
		switch resp.Tag {
		case "*":
			c.observe(resp)
			untagged = append(untagged, resp)
			continue
		case "+":
			continue // continuation request; our commands carry no literals
		}
		if resp.Tag != tag {
			continue // stray response to another tag; ignore
		}
		if resp.Type != "OK" {
			return untagged, &CmdError{Cmd: cmd, Status: resp.Type, Text: respText(resp)}
		}
		return untagged, nil
	}
}

func (c *Client) observe(r *Response) {
	switch r.Type {
	case "EXISTS":
		c.Exists = r.Seq
	case "EXPUNGE":
		c.Expunged++
	}
}

// Logout terminates the session; errors are ignored on purpose.
func (c *Client) Logout() {
	_, _ = c.Do("LOGOUT")
}

// selectInfo carries the SELECT (CONDSTORE) parameters the syncer needs.
type selectInfo struct {
	uidValidity   uint64
	highestModSeq uint64 // 0 when the server does not support CONDSTORE
	exists        int
}

// Select selects a folder with CONDSTORE enabled.
func (c *Client) Select(folder string) (*selectInfo, error) {
	untagged, err := c.Do(fmt.Sprintf("SELECT %s (CONDSTORE)", quote(folder)))
	if err != nil {
		return nil, err
	}
	info := &selectInfo{exists: c.Exists}
	for _, r := range untagged {
		if r.Type != "OK" {
			continue
		}
		code, _ := respCode(r.Fields)
		if len(code) < 2 {
			continue
		}
		kw, _ := code[0].(string)
		v, _ := toUint(code[1])
		switch strings.ToUpper(kw) {
		case "UIDVALIDITY":
			info.uidValidity = v
		case "HIGHESTMODSEQ":
			info.highestModSeq = v
		}
	}
	if info.uidValidity == 0 {
		return nil, fmt.Errorf("imapsync: server did not report UIDVALIDITY for %q", folder)
	}
	return info, nil
}

// fetchItem is one parsed untagged FETCH response.
type fetchItem struct {
	UID    uint64
	Flags  []string
	ModSeq uint64
}

// parseFetch extracts UID/FLAGS/MODSEQ from an untagged FETCH response.
func parseFetch(r *Response) (fetchItem, bool) {
	var it fetchItem
	if r.Type != "FETCH" || len(r.Fields) == 0 {
		return it, false
	}
	lst, ok := r.Fields[0].(List)
	if !ok {
		return it, false
	}
	for i := 0; i+1 < len(lst); i += 2 {
		key, _ := lst[i].(string)
		switch strings.ToUpper(key) {
		case "UID":
			it.UID, _ = toUint(lst[i+1])
		case "FLAGS":
			if fl, ok := lst[i+1].(List); ok {
				for _, f := range fl {
					if s, ok := f.(string); ok {
						it.Flags = append(it.Flags, s)
					}
				}
			}
		case "MODSEQ":
			if ml, ok := lst[i+1].(List); ok && len(ml) > 0 {
				it.ModSeq, _ = toUint(ml[0])
			}
		}
	}
	return it, it.UID != 0
}

// uidFetch issues a UID FETCH for flags and returns the parsed items.
// changedSince > 0 appends a CONDSTORE CHANGEDSINCE modifier.
func (c *Client) uidFetch(changedSince uint64) ([]fetchItem, error) {
	cmd := "UID FETCH 1:* (FLAGS)"
	if changedSince > 0 {
		cmd = fmt.Sprintf("UID FETCH 1:* (FLAGS) (CHANGEDSINCE %d)", changedSince)
	}
	untagged, err := c.Do(cmd)
	if err != nil {
		return nil, err
	}
	var items []fetchItem
	for _, r := range untagged {
		if it, ok := parseFetch(r); ok {
			items = append(items, it)
		}
	}
	return items, nil
}

// uidSearchAll returns the UIDs of all messages currently in the folder.
func (c *Client) uidSearchAll() (map[uint64]bool, error) {
	untagged, err := c.Do("UID SEARCH ALL")
	if err != nil {
		return nil, err
	}
	live := map[uint64]bool{}
	for _, r := range untagged {
		if r.Type != "SEARCH" {
			continue
		}
		for _, f := range r.Fields {
			if u, ok := toUint(f); ok {
				live[u] = true
			}
		}
	}
	return live, nil
}

// respCode extracts a [...] response code from the front of parsed fields.
// Brackets are ordinary atom characters to the tokenizer (so BODY[HEADER]
// survives), so a code arrives as atoms like "[UIDVALIDITY" "1000]".
func respCode(fields []interface{}) (code List, rest []interface{}) {
	if len(fields) == 0 {
		return nil, fields
	}
	s, ok := fields[0].(string)
	if !ok || !strings.HasPrefix(s, "[") {
		return nil, fields
	}
	first := strings.TrimPrefix(s, "[")
	rest = fields[1:]
	if strings.HasSuffix(first, "]") { // single-token code, e.g. [ALERT]
		return List{strings.TrimSuffix(first, "]")}, rest
	}
	code = List{first}
	for i, f := range rest {
		if str, ok := f.(string); ok && strings.HasSuffix(str, "]") {
			code = append(code, strings.TrimSuffix(str, "]"))
			return code, rest[i+1:]
		}
		code = append(code, f)
	}
	return code, nil // unterminated code; tolerate
}

// respText joins the human-readable text of a status response.
func respText(r *Response) string {
	_, rest := respCode(r.Fields)
	var parts []string
	for _, f := range rest {
		if s, ok := f.(string); ok {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, " ")
}

// quote renders s as an IMAP quoted string.
func quote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + r.Replace(s) + `"`
}
