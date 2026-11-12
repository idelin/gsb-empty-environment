package netsec

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// Credential is a login and password pair from a netrc file.
type Credential struct {
	Login    string
	Password string
}

// Netrc is a parsed netrc file. Machine lookups use exact, normalized
// hostnames; the default entry is kept separately so callers can explicitly
// decide where it is safe to use.
type Netrc struct {
	machines   map[string]Credential
	defaultNC  Credential
	hasDefault bool
}

type netrcEntry struct {
	host        string
	isDefault   bool
	credential  Credential
	hasLogin    bool
	hasPassword bool
}

// ParseNetrc parses a netrc file from r.
func ParseNetrc(r io.Reader) (*Netrc, error) {
	if r == nil {
		return nil, fmt.Errorf("netsec: nil netrc reader")
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("netsec: read netrc: %w", err)
	}
	return parseNetrcBytes(data)
}

// ParseNetrcString parses netrc data held in memory.
func ParseNetrcString(data string) (*Netrc, error) {
	return parseNetrcBytes([]byte(data))
}

// ParseNetrcFile parses the netrc file at path.
func ParseNetrcFile(path string) (*Netrc, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("netsec: open netrc: %w", err)
	}
	defer f.Close()
	return ParseNetrc(f)
}

// LoadNetrc is an alias for ParseNetrcFile.
func LoadNetrc(path string) (*Netrc, error) {
	return ParseNetrcFile(path)
}

// Lookup returns credentials for an exact machine entry. It never falls
// back to the default entry.
func (n *Netrc) Lookup(host string) (Credential, bool) {
	if n == nil || len(n.machines) == 0 {
		return Credential{}, false
	}
	normalized, err := normalizeHost(host)
	if err != nil {
		return Credential{}, false
	}
	credential, ok := n.machines[normalized]
	return credential, ok
}

// Default returns the netrc default entry, if one was defined.
func (n *Netrc) Default() (Credential, bool) {
	if n == nil || !n.hasDefault {
		return Credential{}, false
	}
	return n.defaultNC, true
}

func parseNetrcBytes(data []byte) (*Netrc, error) {
	decoded := strings.ReplaceAll(string(data), "\r\n", "\n")
	decoded = strings.ReplaceAll(decoded, "\r", "\n")
	decoded = strings.TrimPrefix(decoded, "\ufeff")
	lines := strings.Split(decoded, "\n")

	result := &Netrc{machines: make(map[string]Credential)}
	var current *netrcEntry
	inMacro := false

	for lineNumber, line := range lines {
		if inMacro {
			if strings.TrimSpace(line) == "" {
				inMacro = false
			}
			continue
		}

		tokens, err := tokenizeNetrcLine(line)
		if err != nil {
			return nil, fmt.Errorf("netsec: netrc line %d: %w", lineNumber+1, err)
		}

		for i := 0; i < len(tokens); i++ {
			switch tokens[i] {
			case "macdef":
				// A macro body is arbitrary text and is not netrc syntax.
				// It terminates at the next physically blank line.
				inMacro = true
				i = len(tokens)
				continue
			case "machine":
				storeCurrent(result, &current)
				i++
				if i >= len(tokens) {
					return nil, fmt.Errorf("netsec: netrc line %d: machine requires a hostname", lineNumber+1)
				}
				normalized, err := normalizeHost(tokens[i])
				if err != nil {
					return nil, fmt.Errorf("netsec: netrc line %d: machine %q: %w", lineNumber+1, tokens[i], err)
				}
				current = &netrcEntry{host: normalized}
			case "default":
				storeCurrent(result, &current)
				current = &netrcEntry{isDefault: true}
			case "login", "password", "account":
				if current == nil {
					i++
					continue
				}
				i++
				if i >= len(tokens) {
					return nil, fmt.Errorf("netsec: netrc line %d: %s requires a value", lineNumber+1, tokens[i-1])
				}
				switch tokens[i-1] {
				case "login":
					current.credential.Login = tokens[i]
					current.hasLogin = true
				case "password":
					current.credential.Password = tokens[i]
					current.hasPassword = true
				}
			}
		}
	}

	storeCurrent(result, &current)
	return result, nil
}

func storeCurrent(n *Netrc, current **netrcEntry) {
	if *current != nil && hasCredentials(*current) {
		storeEntry(n, *current)
	}
	*current = nil
}

func hasCredentials(entry *netrcEntry) bool {
	return entry.hasLogin || entry.hasPassword
}

func storeEntry(n *Netrc, entry *netrcEntry) {
	switch {
	case entry.isDefault:
		n.defaultNC = entry.credential
		n.hasDefault = true
	case entry.host != "":
		n.machines[entry.host] = entry.credential
	}
}

func tokenizeNetrcLine(line string) ([]string, error) {
	if strings.HasPrefix(strings.TrimSpace(line), "#") {
		return nil, nil
	}

	var tokens []string

	for i := 0; i < len(line); {
		switch line[i] {
		case ' ', '\t', '\v', '\f':
			i++
			continue
		case '"':
			i++
			var value strings.Builder
			closed := false
			for i < len(line) {
				if line[i] == '"' {
					i++
					closed = true
					break
				}
				if line[i] == '\\' && i+1 < len(line) {
					next := line[i+1]
					if next == '"' || next == '\\' {
						value.WriteByte(next)
						i += 2
						continue
					}
				}
				value.WriteByte(line[i])
				i++
			}
			if !closed {
				return nil, fmt.Errorf("unterminated quoted value")
			}
			tokens = append(tokens, value.String())
		default:
			start := i
			for i < len(line) && !isNetrcSpace(line[i]) {
				i++
			}
			tokens = append(tokens, line[start:i])
		}
	}

	return tokens, nil
}

func isNetrcSpace(b byte) bool {
	switch b {
	case ' ', '\t', '\v', '\f', '\r':
		return true
	default:
		return false
	}
}
