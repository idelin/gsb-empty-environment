// Command candidatekeys enumerates all minimal candidate keys of a relation
// given a set of functional dependencies (at most 24 attributes).
//
// Input format (file argument or stdin), one FD per line:
//
//	A, B -> C
//	C -> D
//	-> X        // empty LHS is allowed
//	# comment
//
// Attributes are names separated by commas and/or whitespace. Attributes that
// appear in no FD (e.g. for the empty-key case) can be declared with -attrs.
//
// Output lists every candidate key ordered by size, then lexicographically.
// For each key it prints the full-attribute closure and, for every member,
// the attributes that become unreachable when that member is removed.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

const maxAttrs = 24

type fd struct {
	lhs uint32
	rhs uint32
}

// schema holds the attribute universe and the FD set.
type schema struct {
	names []string          // sorted attribute names
	index map[string]uint32 // name -> bit
	fds   []fd
	full  uint32
}

// splitNames splits a comma/whitespace separated attribute list.
func splitNames(list string) []string {
	return strings.FieldsFunc(list, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t'
	})
}

// buildSchema constructs a schema from attribute names and raw FD strings of
// the form "lhs -> rhs". Attribute bits are assigned in sorted-name order so
// that lexicographic enumeration matches alphabetical order.
func buildSchema(attrNames []string, rawFDs [][2]string) (*schema, error) {
	set := make(map[string]bool)
	for _, a := range attrNames {
		set[a] = true
	}
	for _, raw := range rawFDs {
		for _, side := range raw {
			for _, name := range splitNames(side) {
				set[name] = true
			}
		}
	}
	if len(set) > maxAttrs {
		return nil, fmt.Errorf("too many attributes: %d (max %d)", len(set), maxAttrs)
	}
	s := &schema{index: make(map[string]uint32)}
	for name := range set {
		s.names = append(s.names, name)
	}
	sort.Strings(s.names)
	for i, name := range s.names {
		s.index[name] = 1 << uint(i)
		s.full |= s.index[name]
	}
	for _, raw := range rawFDs {
		var f fd
		for _, name := range splitNames(raw[0]) {
			f.lhs |= s.index[name]
		}
		for _, name := range splitNames(raw[1]) {
			f.rhs |= s.index[name]
		}
		s.fds = append(s.fds, f)
	}
	return s, nil
}

// closure computes the attribute closure of mask under the FD set.
func (s *schema) closure(mask uint32) uint32 {
	for {
		grown := mask
		for _, f := range s.fds {
			if mask&f.lhs == f.lhs {
				grown |= f.rhs
			}
		}
		if grown == mask {
			return mask
		}
		mask = grown
	}
}

// containsKey reports whether mask contains an already-found candidate key.
// Any such mask is a non-minimal superkey and is pruned, together with all
// of its supersets (they are generated later in size order).
func containsKey(mask uint32, keys []uint32) bool {
	for _, k := range keys {
		if mask&k == k {
			return true
		}
	}
	return false
}

// candidateKeys enumerates all minimal superkeys ordered by size, then
// lexicographically by attribute name. Enumeration is by increasing size so
// that any superkey containing a previously found key can be pruned; cyclic
// dependencies (A -> B, B -> A) are handled naturally because every minimal
// superkey is tested directly against the closure.
func (s *schema) candidateKeys() []uint32 {
	var keys []uint32
	n := uint(len(s.names))
	for size := uint(0); size <= n; size++ {
		forEachCombo(n, size, func(mask uint32) {
			if containsKey(mask, keys) {
				return
			}
			if s.closure(mask) == s.full {
				keys = append(keys, mask)
			}
		})
	}
	return keys
}

// forEachCombo calls fn for every size-k subset of {0..n-1}, in
// lexicographic order of the (sorted) attribute indices.
func forEachCombo(n, k uint, fn func(mask uint32)) {
	if k > n {
		return
	}
	idx := make([]uint, k)
	for i := range idx {
		idx[i] = uint(i)
	}
	for {
		var mask uint32
		for _, i := range idx {
			mask |= 1 << i
		}
		fn(mask)
		if k == 0 {
			return
		}
		// Find the rightmost index that can be incremented.
		i := k - 1
		for idx[i] == n-k+i {
			if i == 0 {
				return
			}
			i--
		}
		idx[i]++
		for j := i + 1; j < k; j++ {
			idx[j] = idx[j-1] + 1
		}
	}
}

// formatSet renders a bitmask as "{A, B}" (or "{}" for the empty set).
func (s *schema) formatSet(mask uint32) string {
	var names []string
	for i, name := range s.names {
		if mask&(1<<uint(i)) != 0 {
			names = append(names, name)
		}
	}
	return "{" + strings.Join(names, ", ") + "}"
}

// report prints all candidate keys with their closures and, for each member,
// the attributes missing from the closure once that member is removed.
func (s *schema) report(w io.Writer, keys []uint32) {
	fmt.Fprintf(w, "attributes (%d): %s\n", len(s.names), strings.Join(s.names, ", "))
	fmt.Fprintf(w, "candidate keys (%d):\n", len(keys))
	for i, k := range keys {
		fmt.Fprintf(w, "%d. %s\n", i+1, s.formatSet(k))
		fmt.Fprintf(w, "   closure: %s\n", s.formatSet(s.closure(k)))
		if k == 0 {
			fmt.Fprintf(w, "   (empty key: no members to remove)\n")
			continue
		}
		for bit := uint(0); bit < uint(len(s.names)); bit++ {
			member := uint32(1) << bit
			if k&member == 0 {
				continue
			}
			missing := s.full &^ s.closure(k &^ member)
			fmt.Fprintf(w, "   remove %s -> missing: %s\n",
				s.names[bit], s.formatSet(missing))
		}
	}
}

// parseInput reads FD lines, stripping comments and blank lines.
func parseInput(in io.Reader) ([][2]string, error) {
	var rawFDs [][2]string
	scanner := bufio.NewScanner(in)
	for scanner.Scan() {
		line := scanner.Text()
		if i := strings.Index(line, "#"); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "->", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("malformed FD (expected \"lhs -> rhs\"): %q", line)
		}
		rawFDs = append(rawFDs, [2]string{parts[0], parts[1]})
	}
	return rawFDs, scanner.Err()
}

func run(in io.Reader, out io.Writer, extraAttrs string) error {
	rawFDs, err := parseInput(in)
	if err != nil {
		return err
	}
	s, err := buildSchema(splitNames(extraAttrs), rawFDs)
	if err != nil {
		return err
	}
	s.report(out, s.candidateKeys())
	return nil
}

func main() {
	attrs := flag.String("attrs", "", "comma-separated attributes to declare in addition to those in the FDs")
	flag.Parse()

	var in io.Reader = os.Stdin
	if flag.NArg() > 1 {
		fmt.Fprintln(os.Stderr, "usage: candidatekeys [-attrs A,B,C] [fd-file]")
		os.Exit(2)
	}
	if flag.NArg() == 1 {
		f, err := os.Open(flag.Arg(0))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		defer f.Close()
		in = f
	}
	if err := run(in, os.Stdout, *attrs); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
