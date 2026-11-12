package netsec

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
)

type smtpReply struct {
	code  int
	lines []string
}

func (r smtpReply) text() string {
	return strings.Join(r.lines, "\n")
}

func readReply(r *bufio.Reader) (smtpReply, error) {
	var reply smtpReply
	for i := 0; i < 200; i++ {
		line, err := r.ReadString('\n')
		if err != nil {
			return smtpReply{}, err
		}
		line = strings.TrimRight(line, "\r\n")
		if len(line) < 3 {
			return smtpReply{}, errors.New("netsec: short SMTP reply")
		}
		code, err := strconv.Atoi(line[:3])
		if err != nil {
			return smtpReply{}, fmt.Errorf("netsec: invalid SMTP reply code %q", line[:3])
		}
		if reply.code == 0 {
			reply.code = code
		} else if reply.code != code {
			return smtpReply{}, errors.New("netsec: inconsistent SMTP multi-line reply")
		}

		if len(line) == 3 {
			return reply, nil
		}
		switch line[3] {
		case '-':
			reply.lines = append(reply.lines, strings.TrimSpace(line[4:]))
			continue
		case ' ':
			reply.lines = append(reply.lines, strings.TrimSpace(line[4:]))
			return reply, nil
		default:
			return smtpReply{}, fmt.Errorf("netsec: invalid SMTP reply separator %q", line[3])
		}
	}
	return smtpReply{}, errors.New("netsec: too many SMTP reply lines")
}

type smtpConn struct {
	conn net.Conn
	r    *bufio.Reader
	w    *bufio.Writer
}

func newSMTPConn(c net.Conn) *smtpConn {
	return &smtpConn{
		conn: c,
		r:    bufio.NewReader(c),
		w:    bufio.NewWriter(c),
	}
}

func (c *smtpConn) close() error {
	return c.conn.Close()
}

func (c *smtpConn) command(line string) (smtpReply, error) {
	if _, err := c.w.WriteString(line + "\r\n"); err != nil {
		return smtpReply{}, err
	}
	if err := c.w.Flush(); err != nil {
		return smtpReply{}, err
	}
	return readReply(c.r)
}

func (c *smtpConn) reply() (smtpReply, error) {
	return readReply(c.r)
}

func capabilities(reply smtpReply) map[string]string {
	caps := make(map[string]string)
	for _, line := range reply.lines {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		key := strings.ToUpper(fields[0])
		value := ""
		if len(fields) > 1 {
			value = strings.Join(fields[1:], " ")
		}
		caps[key] = value
	}
	return caps
}

func hasCapability(reply smtpReply, name string) bool {
	_, ok := capabilities(reply)[strings.ToUpper(name)]
	return ok
}
