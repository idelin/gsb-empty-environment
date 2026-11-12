package netsec

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
)

const defaultMaxMessageBytes = 32 << 20

// InboundServer accepts SMTP submissions and enforces RFC 8689 on MAIL FROM.
// When TLSConfig is non-nil it advertises STARTTLS. REQUIRETLS is advertised
// in both cleartext and TLS EHLO replies, as RFC 8689 requires, but the
// parameter is accepted only after STARTTLS has completed.
type InboundServer struct {
	Hostname        string
	TLSConfig       *tls.Config
	Handler         InboundHandler
	MaxMessageBytes int64
}

// ServeListener accepts connections until the listener is closed.
func (s *InboundServer) ServeListener(l net.Listener) error {
	for {
		conn, err := l.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		go func() {
			_ = s.ServeConn(conn)
		}()
	}
}

// ServeConn runs one inbound SMTP session. It always closes the connection.
func (s *InboundServer) ServeConn(raw net.Conn) error {
	conn := raw
	defer func() { _ = conn.Close() }()

	r := bufio.NewReader(conn)
	w := bufio.NewWriter(conn)

	hostname := s.Hostname
	if hostname == "" {
		hostname = "localhost"
	}
	if err := writeReply(w, 220, hostname+" ESMTP ready"); err != nil {
		return err
	}

	var (
		greeted    bool
		tlsActive  bool
		haveMail   bool
		envelope   Envelope
		clientAddr = raw.RemoteAddr().String()
	)

	resetTransaction := func() {
		haveMail = false
		envelope = Envelope{}
	}

	for {
		line, err := r.ReadString('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if err := writeReply(w, 500, "5.5.1 empty command"); err != nil {
				return err
			}
			continue
		}

		cmd, arg := splitCommand(line)
		switch cmd {
		case "EHLO":
			greeted = true
			resetTransaction()
			if err := writeEHLO(w, hostname, s.TLSConfig != nil && !tlsActive); err != nil {
				return err
			}

		case "HELO":
			greeted = true
			resetTransaction()
			if err := writeReply(w, 250, hostname); err != nil {
				return err
			}

		case "STARTTLS":
			if arg != "" {
				if err := writeReply(w, 501, "5.5.2 STARTTLS takes no arguments"); err != nil {
					return err
				}
				continue
			}
			if s.TLSConfig == nil {
				if err := writeReply(w, 502, "5.5.1 STARTTLS not supported"); err != nil {
					return err
				}
				continue
			}
			if tlsActive {
				if err := writeReply(w, 503, "5.5.1 TLS already active"); err != nil {
					return err
				}
				continue
			}
			if err := writeReply(w, 220, "2.0.0 ready to start TLS"); err != nil {
				return err
			}
			tlsConn := tls.Server(conn, s.TLSConfig)
			if err := tlsConn.Handshake(); err != nil {
				return err
			}
			conn = tlsConn
			r = bufio.NewReader(conn)
			w = bufio.NewWriter(conn)
			tlsActive = true
			greeted = false
			resetTransaction()

		case "MAIL":
			if !greeted {
				if err := writeReply(w, 503, "5.5.1 send EHLO first"); err != nil {
					return err
				}
				continue
			}
			if haveMail {
				if err := writeReply(w, 503, "5.5.1 nested MAIL command"); err != nil {
					return err
				}
				continue
			}
			addr, params, err := parsePathCommand(arg, "FROM")
			if err != nil {
				if err := writeReply(w, 501, "5.5.2 "+err.Error()); err != nil {
					return err
				}
				continue
			}
			_, requireTLS := params["REQUIRETLS"]
			if requireTLS && !tlsActive {
				if err := writeReply(w, 550, "5.7.10 REQUIRETLS requires an established TLS session"); err != nil {
					return err
				}
				continue
			}
			envelope = Envelope{MailFrom: addr, RequireTLS: requireTLS}
			haveMail = true
			if err := writeReply(w, 250, "2.1.0 sender accepted"); err != nil {
				return err
			}

		case "RCPT":
			if !haveMail {
				if err := writeReply(w, 503, "5.5.1 need MAIL before RCPT"); err != nil {
					return err
				}
				continue
			}
			addr, _, err := parsePathCommand(arg, "TO")
			if err != nil {
				if err := writeReply(w, 501, "5.5.2 "+err.Error()); err != nil {
					return err
				}
				continue
			}
			envelope.RcptTo = append(envelope.RcptTo, addr)
			if err := writeReply(w, 250, "2.1.5 recipient accepted"); err != nil {
				return err
			}

		case "DATA":
			if arg != "" {
				if err := writeReply(w, 501, "5.5.2 DATA takes no arguments"); err != nil {
					return err
				}
				continue
			}
			if !haveMail || len(envelope.RcptTo) == 0 {
				if err := writeReply(w, 503, "5.5.1 need MAIL and RCPT before DATA"); err != nil {
					return err
				}
				continue
			}
			if err := writeReply(w, 354, "3.0.0 end with <CRLF>.<CRLF>"); err != nil {
				return err
			}
			data, tooLarge, err := readMessageData(r, s.maxMessageBytes())
			if err != nil {
				return err
			}
			if tooLarge {
				resetTransaction()
				if err := writeReply(w, 552, "5.3.4 message too large"); err != nil {
					return err
				}
				continue
			}
			if err := ValidateTLSRequirement(data, envelope.RequireTLS); err != nil {
				resetTransaction()
				if err := writeReply(w, 554, "5.6.0 "+err.Error()); err != nil {
					return err
				}
				continue
			}
			envelope.Data = data
			msg := &InboundMessage{Envelope: envelope, TLS: tlsActive, RemoteAddr: clientAddr}
			if s.Handler != nil {
				if err := s.Handler(msg); err != nil {
					resetTransaction()
					if rerr := writeReply(w, 451, "4.3.0 "+err.Error()); rerr != nil {
						return rerr
					}
					continue
				}
			}
			resetTransaction()
			if err := writeReply(w, 250, "2.0.0 queued"); err != nil {
				return err
			}

		case "RSET":
			resetTransaction()
			if err := writeReply(w, 250, "2.0.0 reset"); err != nil {
				return err
			}

		case "NOOP":
			if err := writeReply(w, 250, "2.0.0 ok"); err != nil {
				return err
			}

		case "QUIT":
			_ = writeReply(w, 221, "2.0.0 bye")
			return nil

		default:
			if err := writeReply(w, 500, "5.5.1 unrecognized command"); err != nil {
				return err
			}
		}
	}
}

func (s *InboundServer) maxMessageBytes() int64 {
	if s.MaxMessageBytes > 0 {
		return s.MaxMessageBytes
	}
	return defaultMaxMessageBytes
}

func splitCommand(line string) (string, string) {
	line = strings.TrimSpace(line)
	i := strings.IndexByte(line, ' ')
	if i < 0 {
		return strings.ToUpper(line), ""
	}
	return strings.ToUpper(line[:i]), strings.TrimSpace(line[i+1:])
}

func writeReply(w *bufio.Writer, code int, text string) error {
	if _, err := fmt.Fprintf(w, "%d %s\r\n", code, text); err != nil {
		return err
	}
	return w.Flush()
}

func writeEHLO(w *bufio.Writer, hostname string, offerStartTLS bool) error {
	lines := []string{hostname, "SIZE 33554432", "8BITMIME", "REQUIRETLS"}
	if offerStartTLS {
		lines = append(lines, "STARTTLS")
	}
	for i, line := range lines {
		sep := "-"
		if i == len(lines)-1 {
			sep = " "
		}
		if _, err := fmt.Fprintf(w, "250%s%s\r\n", sep, line); err != nil {
			return err
		}
	}
	return w.Flush()
}

// parsePathCommand parses "FROM:<path> [PARAM=value ...]" or
// "TO:<path> [PARAM=value ...]".
func parsePathCommand(arg, keyword string) (string, map[string]string, error) {
	arg = strings.TrimSpace(arg)
	prefix := keyword + ":"
	if len(arg) < len(prefix) || !strings.EqualFold(arg[:len(prefix)], prefix) {
		return "", nil, fmt.Errorf("expected %s:<path>", keyword)
	}
	rest := strings.TrimSpace(arg[len(prefix):])
	if !strings.HasPrefix(rest, "<") {
		return "", nil, errors.New("missing angle brackets around path")
	}
	end := strings.IndexByte(rest, '>')
	if end < 0 {
		return "", nil, errors.New("unterminated angle brackets")
	}
	addr := rest[1:end]
	params := make(map[string]string)
	for _, field := range strings.Fields(rest[end+1:]) {
		key, value := field, ""
		if eq := strings.IndexByte(field, '='); eq >= 0 {
			key, value = field[:eq], field[eq+1:]
		}
		key = strings.ToUpper(key)
		if key == "" {
			continue
		}
		if _, dup := params[key]; dup {
			return "", nil, fmt.Errorf("duplicate %s parameter", key)
		}
		if key == "REQUIRETLS" && value != "" {
			return "", nil, errors.New("REQUIRETLS does not take a value")
		}
		params[key] = value
	}
	return addr, params, nil
}

func readMessageData(r *bufio.Reader, maxBytes int64) ([]byte, bool, error) {
	var buf bytes.Buffer
	tooLarge := false
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil, false, io.ErrUnexpectedEOF
			}
			return nil, false, err
		}
		if line == ".\r\n" || line == ".\n" {
			return buf.Bytes(), tooLarge, nil
		}
		if strings.HasPrefix(line, ".") {
			line = line[1:]
		}
		if !tooLarge && int64(buf.Len()+len(line)) > maxBytes {
			tooLarge = true
			buf.Reset()
		}
		if !tooLarge {
			_, _ = buf.WriteString(line)
		}
	}
}
