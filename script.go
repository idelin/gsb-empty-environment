package main

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Script is a parsed event script.
type Script struct {
	Tuples   map[string]Tuple
	Commands []Command
	Delay    int // default link delay (ticks)
	TimeWait int // TIME_WAIT duration (ticks)
}

// ParseScript parses the line-based script format:
//
//	CONN <id> <client-ip:port> <server-ip:port>
//	SET delay=<n> timewait=<n>
//	<tick> SYN  <id> <dir> isn=<n> [delay=<n>]
//	<tick> DATA <id> <dir> seq=<n> "payload" [delay=<n>]
//	<tick> FIN  <id> <dir> [seq=<n>] [delay=<n>]
//	<tick> RST  <id> <dir> [delay=<n>]
//
// where <dir> is c->s or s->c. '#' starts a comment (outside quotes).
func ParseScript(r io.Reader) (*Script, error) {
	s := &Script{Tuples: make(map[string]Tuple), Delay: 1, TimeWait: 10}
	sc := bufio.NewScanner(r)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		toks, err := tokenize(stripComment(sc.Text()))
		if err != nil {
			return nil, fmt.Errorf("line %d: %v", lineNo, err)
		}
		if len(toks) == 0 {
			continue
		}
		switch toks[0] {
		case "CONN":
			if len(toks) != 4 {
				return nil, fmt.Errorf("line %d: CONN needs an id and two ip:port endpoints", lineNo)
			}
			tuple, err := parseTuple(toks[2], toks[3])
			if err != nil {
				return nil, fmt.Errorf("line %d: %v", lineNo, err)
			}
			if _, dup := s.Tuples[toks[1]]; dup {
				return nil, fmt.Errorf("line %d: duplicate connection id %q", lineNo, toks[1])
			}
			s.Tuples[toks[1]] = tuple
		case "SET":
			if err := s.applySet(toks[1:]); err != nil {
				return nil, fmt.Errorf("line %d: %v", lineNo, err)
			}
		default:
			cmd, err := parseCommand(toks, s.Tuples)
			if err != nil {
				return nil, fmt.Errorf("line %d: %v", lineNo, err)
			}
			s.Commands = append(s.Commands, cmd)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Script) applySet(kvs []string) error {
	if len(kvs) == 0 {
		return fmt.Errorf("SET needs key=value pairs")
	}
	for _, kv := range kvs {
		k, v, err := splitKV(kv)
		if err != nil {
			return err
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return fmt.Errorf("bad value in %q", kv)
		}
		switch k {
		case "delay":
			s.Delay = n
		case "timewait":
			s.TimeWait = n
		default:
			return fmt.Errorf("unknown SET key %q", k)
		}
	}
	return nil
}

func parseCommand(toks []string, tuples map[string]Tuple) (Command, error) {
	if len(toks) < 4 {
		return Command{}, fmt.Errorf("event needs: <tick> <KIND> <conn> <dir> [args]")
	}
	tick, err := strconv.Atoi(toks[0])
	if err != nil || tick < 0 {
		return Command{}, fmt.Errorf("bad tick %q", toks[0])
	}
	kind := strings.ToUpper(toks[1])
	if _, ok := tuples[toks[2]]; !ok {
		return Command{}, fmt.Errorf("unknown connection id %q", toks[2])
	}
	dir, err := parseDir(toks[3])
	if err != nil {
		return Command{}, err
	}
	cmd := Command{Tick: tick, Kind: kind, Conn: toks[2], Dir: dir}
	for _, t := range toks[4:] {
		if strings.HasPrefix(t, "\"") {
			v, err := strconv.Unquote(t)
			if err != nil {
				return Command{}, fmt.Errorf("bad quoted payload: %v", err)
			}
			cmd.Payload = []byte(v)
			continue
		}
		k, v, err := splitKV(t)
		if err != nil {
			return Command{}, err
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return Command{}, fmt.Errorf("bad value in %q", t)
		}
		switch k {
		case "isn", "seq":
			cmd.Seq, cmd.HasSeq = n, true
		case "delay":
			cmd.Delay, cmd.HasDelay = n, true
		default:
			return Command{}, fmt.Errorf("unknown key %q", k)
		}
	}
	switch kind {
	case "SYN":
		if !cmd.HasSeq {
			return Command{}, fmt.Errorf("SYN needs isn=")
		}
	case "DATA":
		if !cmd.HasSeq {
			return Command{}, fmt.Errorf("DATA needs seq=")
		}
		if len(cmd.Payload) == 0 {
			return Command{}, fmt.Errorf("DATA needs a quoted payload")
		}
	case "FIN", "RST":
	default:
		return Command{}, fmt.Errorf("unknown event kind %q", kind)
	}
	return cmd, nil
}

func parseDir(tok string) (Dir, error) {
	switch tok {
	case "c->s":
		return C2S, nil
	case "s->c":
		return S2C, nil
	}
	return 0, fmt.Errorf("bad direction %q (want c->s or s->c)", tok)
}

func parseTuple(client, server string) (Tuple, error) {
	cip, cport, err := parseAddr(client)
	if err != nil {
		return Tuple{}, err
	}
	sip, sport, err := parseAddr(server)
	if err != nil {
		return Tuple{}, err
	}
	return Tuple{ClientIP: cip, ClientPort: cport, ServerIP: sip, ServerPort: sport}, nil
}

func parseAddr(s string) (string, int, error) {
	i := strings.LastIndex(s, ":")
	if i < 0 {
		return "", 0, fmt.Errorf("bad address %q (want ip:port)", s)
	}
	port, err := strconv.Atoi(s[i+1:])
	if err != nil || port < 0 || port > 65535 {
		return "", 0, fmt.Errorf("bad port in %q", s)
	}
	return s[:i], port, nil
}

func splitKV(tok string) (string, string, error) {
	i := strings.Index(tok, "=")
	if i <= 0 {
		return "", "", fmt.Errorf("bad token %q (want key=value)", tok)
	}
	return tok[:i], tok[i+1:], nil
}

// stripComment removes a '#' comment, honoring quoted strings.
func stripComment(line string) string {
	inQuote := false
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '\\':
			if inQuote {
				i++
			}
		case '"':
			inQuote = !inQuote
		case '#':
			if !inQuote {
				return line[:i]
			}
		}
	}
	return line
}

// tokenize splits on whitespace, keeping "quoted strings" (with escapes)
// as single tokens.
func tokenize(line string) ([]string, error) {
	var toks []string
	i := 0
	for i < len(line) {
		if line[i] == ' ' || line[i] == '\t' {
			i++
			continue
		}
		if line[i] == '"' {
			j := i + 1
			closed := false
			for j < len(line) {
				if line[j] == '\\' {
					j += 2
					continue
				}
				if line[j] == '"' {
					closed = true
					j++
					break
				}
				j++
			}
			if !closed {
				return nil, fmt.Errorf("unterminated quoted string")
			}
			toks = append(toks, line[i:j])
			i = j
			continue
		}
		j := i
		for j < len(line) && line[j] != ' ' && line[j] != '\t' {
			j++
		}
		toks = append(toks, line[i:j])
		i = j
	}
	return toks, nil
}
