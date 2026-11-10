package main

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// parseTrace reads the replay trace. Each non-comment line is one exec,
// capset or prctl operation.
func parseTrace(r io.Reader) ([]Event, error) {
	var events []Event
	scanner := bufio.NewScanner(r)
	lineNo := 0
	sawInit := false
	for scanner.Scan() {
		lineNo++
		raw := strings.TrimSpace(scanner.Text())
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		fields := strings.Fields(raw)
		kind := fields[0]
		rest := fields[1:]

		var ev Event
		ev.Line = lineNo
		ev.Kind = kind

		switch kind {
		case "init":
			if sawInit {
				return nil, fmt.Errorf("line %d: duplicate init line", lineNo)
			}
			if lineNo > 1 && len(events) != 0 {
				return nil, fmt.Errorf("line %d: init must precede all other operations", lineNo)
			}
			if err := fillSets(rest, &ev); err != nil {
				return nil, fmt.Errorf("line %d: %w", lineNo, err)
			}
			sawInit = true
		case "exec":
			if len(rest) < 1 {
				return nil, fmt.Errorf("line %d: exec requires a path", lineNo)
			}
			ev.Target = rest[0]
			if err := fillExec(rest[1:], &ev); err != nil {
				return nil, fmt.Errorf("line %d: %w", lineNo, err)
			}
		case "capset":
			if err := fillSets(rest, &ev); err != nil {
				return nil, fmt.Errorf("line %d: %w", lineNo, err)
			}
		case "ambient":
			if err := fillAmbient(rest, &ev); err != nil {
				return nil, fmt.Errorf("line %d: %w", lineNo, err)
			}
		case "bset":
			if err := fillBset(rest, &ev); err != nil {
				return nil, fmt.Errorf("line %d: %w", lineNo, err)
			}
		default:
			return nil, fmt.Errorf("line %d: unknown operation %q", lineNo, kind)
		}
		events = append(events, ev)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return events, nil
}

// flagSet splits "key=value" tokens from bare boolean flags.
type flagSet struct {
	values map[string]string
	bools  map[string]bool
}

func parseFlags(tokens []string) (flagSet, error) {
	fs := flagSet{values: map[string]string{}, bools: map[string]bool{}}
	for _, tok := range tokens {
		if key, val, ok := strings.Cut(tok, "="); ok {
			fs.values[strings.ToLower(key)] = val
			continue
		}
		fs.bools[strings.ToLower(tok)] = true
	}
	return fs, nil
}

func fillSets(tokens []string, ev *Event) error {
	fs, err := parseFlags(tokens)
	if err != nil {
		return err
	}
	for key, dst := range map[string]*CapMask{"p": &ev.P, "i": &ev.I, "e": &ev.E} {
		raw, ok := fs.values[key]
		if !ok {
			return fmt.Errorf("missing required %s=<caps> field", strings.ToUpper(key))
		}
		m, err := parseCapSet(raw)
		if err != nil {
			return err
		}
		*dst = m
	}
	if raw, ok := fs.values["b"]; ok {
		m, err := parseCapSet(raw)
		if err != nil {
			return err
		}
		ev.B = m
		ev.boundingProvided = true
	}
	if len(fs.bools) != 0 {
		return fmt.Errorf("unexpected bare flags: %s", strings.Join(mapKeys(fs.bools), ","))
	}
	return nil
}

func fillExec(tokens []string, ev *Event) error {
	fs, err := parseFlags(tokens)
	if err != nil {
		return err
	}
	for key, dst := range map[string]*CapMask{"fp": &ev.FP, "fi": &ev.FI} {
		if raw, ok := fs.values[key]; ok {
			m, err := parseCapSet(raw)
			if err != nil {
				return err
			}
			*dst = m
		}
	}
	if raw, ok := fs.values["fe"]; ok {
		switch strings.ToLower(raw) {
		case "1", "true":
			ev.FE = true
		case "0", "false":
			ev.FE = false
		default:
			return fmt.Errorf("fE must be 0 or 1, got %q", raw)
		}
	}
	ev.SUID = fs.bools["suid"]
	ev.SGID = fs.bools["sgid"]
	ev.Nosuid = fs.bools["nosuid"]
	for flag := range fs.bools {
		switch flag {
		case "suid", "sgid", "nosuid":
		default:
			return fmt.Errorf("unknown exec flag %q", flag)
		}
	}
	for key := range fs.values {
		switch key {
		case "fp", "fi", "fe":
		default:
			return fmt.Errorf("unknown exec field %q", key)
		}
	}
	return nil
}

func fillAmbient(tokens []string, ev *Event) error {
	if len(tokens) < 1 {
		return fmt.Errorf("ambient requires raise|lower|drop")
	}
	ev.Mode = strings.ToLower(tokens[0])
	switch ev.Mode {
	case "raise", "lower":
		if len(tokens) < 2 {
			return fmt.Errorf("ambient %s requires a capability list", ev.Mode)
		}
		m, err := parseCapSet(strings.Join(tokens[1:], ","))
		if err != nil {
			return err
		}
		ev.Caps = m
	case "drop":
		// drop = PR_CAP_AMBIENT_CLEAR_ALL, takes no capability arguments.
		if len(tokens) > 1 {
			return fmt.Errorf("ambient drop clears the whole ambient set and takes no capability list")
		}
	default:
		return fmt.Errorf("ambient mode must be raise, lower or drop")
	}
	return nil
}

func fillBset(tokens []string, ev *Event) error {
	if len(tokens) < 2 || strings.ToLower(tokens[0]) != "drop" {
		return fmt.Errorf("bset syntax: bset drop <caps>")
	}
	m, err := parseCapSet(strings.Join(tokens[1:], ","))
	if err != nil {
		return err
	}
	ev.Mode = "drop"
	ev.Caps = m
	return nil
}

func mapKeys(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
