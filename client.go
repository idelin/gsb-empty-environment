package netsec

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strings"
	"time"
)

// DialFunc matches net.Dialer.DialContext and lets callers inject a dialer.
type DialFunc func(ctx context.Context, network, address string) (net.Conn, error)

// Target is the next-hop SMTP endpoint. ServerName overrides the TLS SNI and
// verification name; when empty the host portion of Address is used.
type Target struct {
	Address    string
	ServerName string
}

// Deliverer performs outbound SMTP delivery with RFC 8689 enforcement.
//
// VerifyCertificate is called after the STARTTLS handshake whenever it is
// non-nil. In that mode the deliverer disables Go's built-in chain check and
// makes the callback responsible for authentication (including hostname
// validation). When the callback is nil, Go's normal TLS verification using
// TLSConfig (or the system roots) is used instead.
type Deliverer struct {
	Dial              DialFunc
	TLSConfig         *tls.Config
	VerifyCertificate func(tls.ConnectionState) error
	HelloName         string
	Timeout           time.Duration
}

// Deliver sends one message to one next hop. A permanent error means the
// message must be bounced; the caller must not retry the message in
// cleartext or against another MX in a degraded mode.
func (d *Deliverer) Deliver(ctx context.Context, target Target, msg *Envelope) error {
	if msg == nil {
		return &DeliveryError{Stage: "policy", Permanent: true, Code: 554, Response: "5.6.0 nil message"}
	}

	data, err := PrepareMessage(msg.Data, msg.RequireTLS)
	if err != nil {
		return &DeliveryError{
			Stage:     "policy",
			Permanent: true,
			Code:      554,
			Response:  "5.6.0 " + err.Error(),
			Err:       err,
		}
	}

	addr := target.Address
	if addr == "" {
		return &DeliveryError{Stage: "dial", Permanent: true, Code: 554, Response: "5.6.0 empty target address"}
	}

	dial := d.Dial
	if dial == nil {
		timeout := d.Timeout
		if timeout == 0 {
			timeout = 5 * time.Minute
		}
		dialer := &net.Dialer{Timeout: timeout}
		dial = dialer.DialContext
	}
	raw, err := dial(ctx, "tcp", addr)
	if err != nil {
		return &DeliveryError{Stage: "dial", Err: err}
	}
	c := newSMTPConn(raw)
	defer func() { _ = c.close() }()

	greeting, err := c.reply()
	if err != nil {
		return &DeliveryError{Stage: "greeting", Err: err}
	}
	if greeting.code != 220 {
		return smtpFailure("greeting", greeting)
	}

	helloName := d.HelloName
	if helloName == "" {
		helloName = "localhost"
	}

	ehlo, err := c.command("EHLO " + helloName)
	if err != nil {
		return &DeliveryError{Stage: "ehlo", Err: err}
	}
	if ehlo.code != 250 {
		return smtpFailure("ehlo", ehlo)
	}
	caps := capabilities(ehlo)
	_, offersStartTLS := caps["STARTTLS"]
	_, offersRequireTLS := caps["REQUIRETLS"]

	if msg.RequireTLS && !offersStartTLS {
		return &DeliveryError{
			Stage:     "starttls-capability",
			Permanent: true,
			Code:      550,
			Response:  "5.7.10 next hop does not offer STARTTLS; REQUIRETLS cannot be satisfied",
		}
	}

	if offersStartTLS {
		startTLS, err := c.command("STARTTLS")
		if err != nil {
			return &DeliveryError{Stage: "starttls", Permanent: msg.RequireTLS, Err: err}
		}
		if startTLS.code != 220 {
			if msg.RequireTLS {
				return &DeliveryError{
					Stage:     "starttls",
					Permanent: true,
					Code:      550,
					Response:  fmt.Sprintf("5.7.10 STARTTLS refused: %d %s", startTLS.code, startTLS.text()),
				}
			}
		} else {
			tlsConn, err := d.handshake(ctx, c.conn, target)
			if err != nil {
				return &DeliveryError{Stage: "tls", Permanent: msg.RequireTLS, Err: err}
			}
			c = newSMTPConn(tlsConn)

			ehlo, err = c.command("EHLO " + helloName)
			if err != nil {
				return &DeliveryError{Stage: "ehlo-after-tls", Permanent: msg.RequireTLS, Err: err}
			}
			if ehlo.code != 250 {
				return smtpFailure("ehlo-after-tls", ehlo)
			}
			caps = capabilities(ehlo)
			_, offersRequireTLS = caps["REQUIRETLS"]
		}
	}

	if msg.RequireTLS && !offersRequireTLS {
		return &DeliveryError{
			Stage:     "requiretls-capability",
			Permanent: true,
			Code:      550,
			Response:  "5.7.10 next hop did not advertise REQUIRETLS after STARTTLS",
		}
	}

	mail := "MAIL FROM:<" + msg.MailFrom + ">"
	if msg.RequireTLS {
		mail += " REQUIRETLS"
	}
	reply, err := c.command(mail)
	if err != nil {
		return &DeliveryError{Stage: "mail-from", Permanent: msg.RequireTLS, Err: err}
	}
	if reply.code != 250 {
		return smtpFailure("mail-from", reply)
	}

	for _, rcpt := range msg.RcptTo {
		reply, err = c.command("RCPT TO:<" + rcpt + ">")
		if err != nil {
			return &DeliveryError{Stage: "rcpt-to", Permanent: msg.RequireTLS, Err: err}
		}
		if reply.code != 250 && reply.code != 251 {
			return smtpFailure("rcpt-to", reply)
		}
	}

	reply, err = c.command("DATA")
	if err != nil {
		return &DeliveryError{Stage: "data", Permanent: msg.RequireTLS, Err: err}
	}
	if reply.code != 354 {
		return smtpFailure("data", reply)
	}

	if err := writeMessageData(c.w, data); err != nil {
		return &DeliveryError{Stage: "data-write", Permanent: msg.RequireTLS, Err: err}
	}
	reply, err = c.reply()
	if err != nil {
		return &DeliveryError{Stage: "data-response", Permanent: msg.RequireTLS, Err: err}
	}
	if reply.code != 250 {
		return smtpFailure("data-response", reply)
	}

	_, _ = c.command("QUIT")
	return nil
}

// DeliverAll tries the supplied next hops in order. It stops immediately on
// a permanent failure, including any REQUIRETLS/TLS policy failure, so the
// message is never retried against another MX in cleartext.
func (d *Deliverer) DeliverAll(ctx context.Context, targets []Target, msg *Envelope) error {
	if len(targets) == 0 {
		return &DeliveryError{Stage: "dial", Permanent: true, Code: 554, Response: "5.6.0 no next hops"}
	}
	var last error
	for _, target := range targets {
		if err := d.Deliver(ctx, target, msg); err != nil {
			if IsPermanent(err) {
				return err
			}
			last = err
			continue
		}
		return nil
	}
	return last
}

func (d *Deliverer) handshake(ctx context.Context, raw net.Conn, target Target) (net.Conn, error) {
	cfg := d.tlsConfig(target)
	tlsConn := tls.Client(raw, cfg)
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		return nil, err
	}
	if d.VerifyCertificate != nil {
		if err := d.VerifyCertificate(tlsConn.ConnectionState()); err != nil {
			_ = tlsConn.Close()
			return nil, err
		}
	}
	return tlsConn, nil
}

func (d *Deliverer) tlsConfig(target Target) *tls.Config {
	var cfg *tls.Config
	if d.TLSConfig != nil {
		cfg = d.TLSConfig.Clone()
	} else {
		cfg = &tls.Config{}
	}
	if cfg.MinVersion == 0 {
		cfg.MinVersion = tls.VersionTLS12
	}
	if target.ServerName != "" {
		cfg.ServerName = target.ServerName
	} else if cfg.ServerName == "" {
		host, _, err := net.SplitHostPort(target.Address)
		if err == nil {
			cfg.ServerName = host
		} else {
			cfg.ServerName = target.Address
		}
	}
	if d.VerifyCertificate != nil {
		// The injected callback is the complete certificate policy.
		cfg.InsecureSkipVerify = true
	}
	return cfg
}

func smtpFailure(stage string, reply smtpReply) error {
	permanent := reply.code >= 500 && reply.code < 600
	if reply.code >= 400 && reply.code < 500 {
		permanent = false
	}
	return &DeliveryError{
		Stage:     stage,
		Code:      reply.code,
		Response:  fmt.Sprintf("%d %s", reply.code, reply.text()),
		Permanent: permanent,
	}
}

func writeMessageData(w *bufio.Writer, data []byte) error {
	normalized := normalizeCRLF(data)
	if len(normalized) == 0 || !bytes.HasSuffix(normalized, []byte("\r\n")) {
		normalized = append(normalized, '\r', '\n')
	}
	for i := 0; i < len(normalized); {
		end := bytes.Index(normalized[i:], []byte("\r\n"))
		if end < 0 {
			break
		}
		line := normalized[i : i+end]
		if len(line) > 0 && line[0] == '.' {
			if err := w.WriteByte('.'); err != nil {
				return err
			}
		}
		if _, err := w.Write(line); err != nil {
			return err
		}
		if _, err := w.WriteString("\r\n"); err != nil {
			return err
		}
		i += end + 2
	}
	if _, err := w.WriteString(".\r\n"); err != nil {
		return err
	}
	return w.Flush()
}

func normalizeCRLF(data []byte) []byte {
	s := strings.ReplaceAll(string(data), "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.ReplaceAll(s, "\n", "\r\n")
	return []byte(s)
}
