package netsec

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/mail"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Attachment is data attached to a message. Filename is raw text; the wire
// representation is generated as quoted ASCII plus RFC 2231 parameter
// continuations.
type Attachment struct {
	Filename    string
	ContentType string
	Data        []byte
}

// Message is an authoritative header and MIME builder. It deliberately has no
// SetHeader or SetContentType method: callers provide values through typed
// methods, and the builder alone chooses field names, MIME boundaries, and the
// top-level content type.
type Message struct {
	mu sync.Mutex

	from        *Address
	replyTo     []Address
	to          []Address
	cc          []Address
	bcc         []Address
	subject     string
	subjectSet  bool
	date        time.Time
	messageID   string
	textBody    string
	attachments []Attachment
	boundary    string
}

// HeaderBuilder is an alias for Message for callers that model the component as
// a header builder.
type HeaderBuilder = Message

// NewMessage returns an empty message with a generated Date. Header and MIME
// generation happen in Build so every produced field has a single owner.
func NewMessage() *Message {
	return &Message{date: time.Now()}
}

// NewHeaderBuilder is an alias constructor for HeaderBuilder.
func NewHeaderBuilder() *Message {
	return NewMessage()
}

// NewMailBuilder is an alias constructor for Message.
func NewMailBuilder() *Message {
	return NewMessage()
}

// SetFrom sets the single From mailbox. Accepts one Address, *Address,
// mail.Address, *mail.Address, or raw RFC 5322 address string. The two-argument
// form SetFrom(displayName, addrSpec) is also accepted.
func (m *Message) SetFrom(values ...any) error {
	var address Address
	var err error
	switch len(values) {
	case 0:
		err = fmt.Errorf("%w: missing From address", ErrInvalidAddress)
	case 1:
		address, err = coerceAddress(values[0])
	case 2:
		name, nameOK := values[0].(string)
		mailbox, mailboxOK := values[1].(string)
		if !nameOK || !mailboxOK {
			err = fmt.Errorf("%w: two-argument From requires a string display name and addr-spec", ErrInvalidAddress)
		} else {
			address, err = NewAddress(name, mailbox)
		}
	default:
		err = fmt.Errorf("%w: From accepts one address or a name/address pair", ErrInvalidAddress)
	}
	if err != nil {
		return fmt.Errorf("From: %w", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.from = &address
	return nil
}

// SetFromNameAddress sets From from a raw display name and bare addr-spec.
func (m *Message) SetFromNameAddress(name, address string) error {
	value, err := NewAddress(name, address)
	if err != nil {
		return fmt.Errorf("From: %w", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.from = &value
	return nil
}

// SetTo replaces the complete To address list.
func (m *Message) SetTo(values ...any) error {
	addresses, err := coerceAddresses(values)
	if err != nil {
		return fmt.Errorf("To: %w", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.to = addresses
	return nil
}

// AddTo appends to the To address list.
func (m *Message) AddTo(values ...any) error {
	addresses, err := coerceAddresses(values)
	if err != nil {
		return fmt.Errorf("To: %w", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.to = append(m.to, addresses...)
	return nil
}

// SetCc replaces the complete Cc address list.
func (m *Message) SetCc(values ...any) error {
	addresses, err := coerceAddresses(values)
	if err != nil {
		return fmt.Errorf("Cc: %w", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cc = addresses
	return nil
}

// AddCc appends to the Cc address list.
func (m *Message) AddCc(values ...any) error {
	addresses, err := coerceAddresses(values)
	if err != nil {
		return fmt.Errorf("Cc: %w", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cc = append(m.cc, addresses...)
	return nil
}

// SetBcc replaces the complete Bcc address list. A sending service that hides
// blind recipients should strip this header before transmission; the envelope
// remains the authoritative RCPT TO source.
func (m *Message) SetBcc(values ...any) error {
	addresses, err := coerceAddresses(values)
	if err != nil {
		return fmt.Errorf("Bcc: %w", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.bcc = addresses
	return nil
}

// SetReplyTo replaces the complete Reply-To address list.
func (m *Message) SetReplyTo(values ...any) error {
	addresses, err := coerceAddresses(values)
	if err != nil {
		return fmt.Errorf("Reply-To: %w", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.replyTo = addresses
	return nil
}

// SetSubject stores raw subject text. It is encoded as one or more RFC 2047
// encoded-words when necessary.
func (m *Message) SetSubject(subject string) error {
	if err := validateText(subject, "subject"); err != nil {
		return err
	}
	if err := rejectPreEncoded(subject); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.subject = subject
	m.subjectSet = true
	return nil
}

// SetTextBody sets the UTF-8 plain-text body. The body is not interpreted as
// headers; line breaks here cannot escape into the header block.
func (m *Message) SetTextBody(body string) error {
	if !utf8.ValidString(body) {
		return fmt.Errorf("%w: body is not valid UTF-8", ErrInvalidHeader)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.textBody = body
	return nil
}

// SetBody is an alias for SetTextBody.
func (m *Message) SetBody(body string) error {
	return m.SetTextBody(body)
}

// AddAttachment validates and adds one attachment. ContentType must be a bare
// media type without parameters; filename and disposition parameters are
// generated by the server.
func (m *Message) AddAttachment(filename, contentType string, data []byte) error {
	if err := validateText(filename, "attachment filename"); err != nil {
		return err
	}
	normalizedType, err := normalizeAttachmentType(contentType)
	if err != nil {
		return err
	}
	attachment := Attachment{
		Filename:    filename,
		ContentType: normalizedType,
		Data:        append([]byte(nil), data...),
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.attachments = append(m.attachments, attachment)
	return nil
}

// Attach is a convenience wrapper for an application/octet-stream attachment.
func (m *Message) Attach(filename string, data []byte) error {
	return m.AddAttachment(filename, defaultAttachmentType, data)
}

// SetDate sets the Date header value. The wire format is generated from the
// time value, never from a caller-provided header string.
func (m *Message) SetDate(value time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.date = value
}

// SetMessageID sets a Message-ID value without angle brackets. It accepts only
// printable ASCII and no whitespace, which keeps the field structured.
func (m *Message) SetMessageID(value string) error {
	if err := validateText(value, "Message-ID"); err != nil {
		return err
	}
	if value == "" || len(value) > 900 {
		return fmt.Errorf("%w: invalid Message-ID length", ErrInvalidHeader)
	}
	for i := 0; i < len(value); i++ {
		if value[i] <= 0x20 || value[i] >= 0x7f {
			return fmt.Errorf("%w: Message-ID contains whitespace or non-ASCII text", ErrInvalidHeader)
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messageID = value
	return nil
}

// Build renders the complete RFC 5322 message. The returned bytes do not share
// storage with the message.
func (m *Message) Build() ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	hasAttachments := len(m.attachments) > 0
	contentType := `text/plain; charset="utf-8"`
	var boundary string
	if hasAttachments {
		var err error
		boundary, err = m.boundaryLocked()
		if err != nil {
			return nil, err
		}
		contentType = `multipart/mixed; boundary="` + boundary + `"`
	}

	headers, err := m.renderHeadersLocked(contentType, hasAttachments)
	if err != nil {
		return nil, err
	}
	buf := bytes.NewBuffer(nil)
	buf.Write(headers)
	buf.WriteString("\r\n")
	if hasAttachments {
		if err := m.renderMultipartLocked(buf, boundary); err != nil {
			return nil, err
		}
	} else {
		buf.WriteString(m.textBody)
	}
	return buf.Bytes(), nil
}

// Bytes is an alias for Build.
func (m *Message) Bytes() ([]byte, error) {
	return m.Build()
}

// Render is an alias for Build.
func (m *Message) Render() ([]byte, error) {
	return m.Build()
}

// HeaderBytes renders the header block, including its final CRLF, without a
// body.
func (m *Message) HeaderBytes() ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	contentType := `text/plain; charset="utf-8"`
	hasAttachments := len(m.attachments) > 0
	if hasAttachments {
		if _, err := m.boundaryLocked(); err != nil {
			return nil, err
		}
		contentType = `multipart/mixed; boundary="` + m.boundary + `"`
	}
	return m.renderHeadersLocked(contentType, hasAttachments)
}

// Boundary returns the boundary that will be used for a multipart message.
func (m *Message) Boundary() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.boundaryLocked()
}

// String returns the rendered message, or an empty string if rendering failed.
// Prefer Build when an error must be handled.
func (m *Message) String() string {
	data, err := m.Build()
	if err != nil {
		return ""
	}
	return string(data)
}

func (m *Message) boundaryLocked() (string, error) {
	for attempt := 0; attempt < 16; attempt++ {
		if m.boundary == "" {
			generated, err := generateBoundary()
			if err != nil {
				return "", err
			}
			m.boundary = generated
		}
		if !boundaryCollides(m.boundary, m.textBody, m.attachments) {
			return m.boundary, nil
		}
		m.boundary = ""
	}
	return "", fmt.Errorf("netsec: could not generate a non-colliding MIME boundary")
}

func (m *Message) renderHeadersLocked(contentType string, multipart bool) ([]byte, error) {
	buf := bytes.NewBuffer(nil)

	if m.from != nil {
		tokens, err := m.from.headerTokens()
		if err != nil {
			return nil, err
		}
		if err := writeFoldedHeader(buf, "From", tokens); err != nil {
			return nil, err
		}
	}
	if len(m.replyTo) > 0 {
		tokens, err := addressTokens(m.replyTo)
		if err != nil {
			return nil, err
		}
		if err := writeFoldedHeader(buf, "Reply-To", tokens); err != nil {
			return nil, err
		}
	}
	if len(m.to) > 0 {
		tokens, err := addressTokens(m.to)
		if err != nil {
			return nil, err
		}
		if err := writeFoldedHeader(buf, "To", tokens); err != nil {
			return nil, err
		}
	}
	if len(m.cc) > 0 {
		tokens, err := addressTokens(m.cc)
		if err != nil {
			return nil, err
		}
		if err := writeFoldedHeader(buf, "Cc", tokens); err != nil {
			return nil, err
		}
	}
	if len(m.bcc) > 0 {
		tokens, err := addressTokens(m.bcc)
		if err != nil {
			return nil, err
		}
		if err := writeFoldedHeader(buf, "Bcc", tokens); err != nil {
			return nil, err
		}
	}
	if m.subjectSet {
		if m.subject == "" {
			buf.WriteString("Subject:\r\n")
		} else {
			tokens, err := encodeUnstructured(m.subject)
			if err != nil {
				return nil, err
			}
			if len(tokens) == 0 {
				tokens = []string{""}
			}
			if err := writeFoldedHeader(buf, "Subject", tokens); err != nil {
				return nil, err
			}
		}
	}

	date := m.date
	if date.IsZero() {
		date = time.Now()
		m.date = date
	}
	if err := writeFoldedHeader(buf, "Date", []string{date.Format(time.RFC1123Z)}); err != nil {
		return nil, err
	}
	messageID := m.messageID
	if messageID == "" {
		generated, err := m.generateMessageIDLocked(date)
		if err != nil {
			return nil, err
		}
		messageID = generated
		m.messageID = generated
	}
	if err := writeFoldedHeader(buf, "Message-ID", []string{"<" + messageID + ">"}); err != nil {
		return nil, err
	}
	if err := writeFoldedHeader(buf, "MIME-Version", []string{"1.0"}); err != nil {
		return nil, err
	}
	if err := writeFoldedHeader(buf, "Content-Type", []string{contentType}); err != nil {
		return nil, err
	}
	if !multipart {
		encoding := "7bit"
		for i := 0; i < len(m.textBody); i++ {
			if m.textBody[i] >= 0x80 {
				encoding = "8bit"
				break
			}
		}
		if err := writeFoldedHeader(buf, "Content-Transfer-Encoding", []string{encoding}); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

func (m *Message) renderMultipartLocked(buf *bytes.Buffer, boundary string) error {
	buf.WriteString("--" + boundary + "\r\n")
	buf.WriteString("Content-Type: text/plain; charset=\"utf-8\"\r\n")
	buf.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	buf.WriteString("\r\n")
	body := normalizeCRLF(m.textBody)
	buf.WriteString(body)
	if body != "" && !strings.HasSuffix(body, "\r\n") {
		buf.WriteString("\r\n")
	}

	for _, attachment := range m.attachments {
		buf.WriteString("--" + boundary + "\r\n")
		if err := writeFoldedHeader(buf, "Content-Type", []string{attachment.ContentType}); err != nil {
			return err
		}
		if err := writeFoldedHeader(buf, "Content-Transfer-Encoding", []string{"base64"}); err != nil {
			return err
		}
		disposition, err := dispositionTokens(attachment.Filename)
		if err != nil {
			return err
		}
		if err := writeFoldedHeader(buf, "Content-Disposition", disposition); err != nil {
			return err
		}
		buf.WriteString("\r\n")
		writeBase64Body(buf, attachment.Data)
	}
	buf.WriteString("--" + boundary + "--\r\n")
	return nil
}

func normalizeCRLF(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	return strings.ReplaceAll(value, "\n", "\r\n")
}

func writeBase64Body(buf *bytes.Buffer, data []byte) {
	encoded := base64.StdEncoding.EncodeToString(data)
	for len(encoded) > 0 {
		size := 76
		if size > len(encoded) {
			size = len(encoded)
		}
		buf.WriteString(encoded[:size])
		buf.WriteString("\r\n")
		encoded = encoded[size:]
	}
}

func (m *Message) generateMessageIDLocked(date time.Time) (string, error) {
	random := make([]byte, 12)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("netsec: generate Message-ID: %w", err)
	}
	domain := "localhost"
	if m.from != nil {
		if at := strings.LastIndexByte(m.from.Address, '@'); at >= 0 && at < len(m.from.Address)-1 {
			domain = m.from.Address[at+1:]
		}
	}
	return fmt.Sprintf("%d.%s@%s", date.UnixNano(), hex.EncodeToString(random), domain), nil
}

func coerceAddress(value any) (Address, error) {
	switch item := value.(type) {
	case Address:
		if err := item.Validate(); err != nil {
			return Address{}, err
		}
		return item, nil
	case *Address:
		if item == nil {
			return Address{}, fmt.Errorf("%w: nil address", ErrInvalidAddress)
		}
		if err := item.Validate(); err != nil {
			return Address{}, err
		}
		return *item, nil
	case string:
		return ParseAddress(item)
	case mail.Address:
		return NewAddress(item.Name, item.Address)
	case *mail.Address:
		if item == nil {
			return Address{}, fmt.Errorf("%w: nil address", ErrInvalidAddress)
		}
		return NewAddress(item.Name, item.Address)
	default:
		return Address{}, fmt.Errorf("%w: unsupported address type %T", ErrInvalidAddress, value)
	}
}

func coerceAddresses(values []any) ([]Address, error) {
	addresses := make([]Address, 0, len(values))
	for _, value := range values {
		switch item := value.(type) {
		case []Address:
			for _, address := range item {
				if err := address.Validate(); err != nil {
					return nil, err
				}
				addresses = append(addresses, address)
			}
		case []*Address:
			for _, address := range item {
				coerced, err := coerceAddress(address)
				if err != nil {
					return nil, err
				}
				addresses = append(addresses, coerced)
			}
		case []string:
			for _, value := range item {
				list, err := ParseAddressList(value)
				if err != nil {
					return nil, err
				}
				addresses = append(addresses, list...)
			}
		case []mail.Address:
			for _, address := range item {
				coerced, err := coerceAddress(address)
				if err != nil {
					return nil, err
				}
				addresses = append(addresses, coerced)
			}
		case string:
			list, err := ParseAddressList(item)
			if err != nil {
				return nil, err
			}
			addresses = append(addresses, list...)
		default:
			address, err := coerceAddress(value)
			if err != nil {
				return nil, err
			}
			addresses = append(addresses, address)
		}
	}
	return addresses, nil
}
