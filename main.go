// Command keyfinder enumerates all candidate keys of a relation given its
// functional dependencies. It supports at most 24 attributes.
//
// Input (file argument or stdin), one FD per line:
//
//	A,B -> C,D
//	C -> A
//	-> D        (empty LHS is allowed)
//
// Attribute names on each side are separated by commas and/or whitespace.
// Lines starting with '#' and blank lines are ignored.
//
// Output lists every candidate key ordered by size and then
// lexicographically, each with its closure (always the full attribute set)
// and, for every member, the attributes that would no longer be derivable
// if that member were removed.
package main

import (
	"bufio"
	"fmt"
	"io"
	"math/bits"
	"os"
	"sort"
	"strings"
)

const maxAttrs = 24

// FD is a functional dependency lhs -> rhs over attribute bitmasks.
type FD struct {
	lhs, rhs uint32
}

// closure returns the attribute closure of set under fds by fixpoint iteration.
func closure(set uint32, fds []FD) uint32 {
	for changed := true; changed; {
		changed = false
		for _, fd := range fds {
			if set&fd.lhs == fd.lhs && set&fd.rhs != fd.rhs {
				set |= fd.rhs
				changed = true
			}
		}
	}
	return set
}

// enumerateKeys returns every candidate key as a bitmask.
//
// It performs a DFS over the subset lattice. Whenever the current set already
// has a full closure, the search does not descend further: any strict
// superset would contain a key within the current set and therefore cannot
// be minimal. This pruning is purely closure-based, so no attribute is ever
// excluded from consideration a priori; in particular attributes that appear
// on right-hand sides (including attributes inside dependency cycles such as
// A -> B, B -> A) are still explored and their keys are found.
//
// Because a set reached with a full closure may still hide a smaller key off
// the search path (e.g. {A,B} reached via A while {B} alone is a key), every
// candidate is verified for minimality afterwards.
func enumerateKeys(n int, fds []FD, full uint32) []uint32 {
	var found []uint32
	var rec func(start int, set uint32)
	rec = func(start int, set uint32) {
		if closure(set, fds) == full {
			found = append(found, set)
			return
		}
		for i := start; i < n; i++ {
			rec(i+1, set|1<<uint(i))
		}
	}
	rec(0, 0)

	keys := found[:0]
	for _, k := range found {
		if isMinimal(k, fds, full) {
			keys = append(keys, k)
		}
	}
	return keys
}

// isMinimal reports whether every proper subset obtained by removing one
// member of k fails to determine the full attribute set.
func isMinimal(k uint32, fds []FD, full uint32) bool {
	for m := k; m != 0; m &= m - 1 {
		bit := m & -m
		if closure(k&^bit, fds) == full {
			return false
		}
	}
	return true
}

// keyLess orders keys by size and then lexicographically by attribute names.
func keyLess(a, b uint32, names []string) bool {
	pa, pb := bits.OnesCount32(a), bits.OnesCount32(b)
	if pa != pb {
		return pa < pb
	}
	ma, mb := a, b
	for ma != 0 || mb != 0 {
		ba, bb := ma&-ma, mb&-mb
		na, nb := names[bits.TrailingZeros32(ba)], names[bits.TrailingZeros32(bb)]
		if na != nb {
			return na < nb
		}
		ma &= ma - 1
		mb &= mb - 1
	}
	return false
}

func formatSet(set uint32, names []string) string {
	if set == 0 {
		return "{}"
	}
	var parts []string
	for m := set; m != 0; m &= m - 1 {
		parts = append(parts, names[bits.TrailingZeros32(m&-m)])
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func parse(r io.Reader) (names []string, fds []FD, err error) {
	index := map[string]uint32{}
	var encounter []string // attribute names in first-encounter order
	attrBit := func(name string) (uint32, error) {
		if b, ok := index[name]; ok {
			return b, nil
		}
		if len(index) == maxAttrs {
			return 0, fmt.Errorf("too many attributes: more than %d", maxAttrs)
		}
		b := uint32(1) << uint(len(encounter))
		index[name] = b
		encounter = append(encounter, name)
		return b, nil
	}
	parseSide := func(s string) (uint32, error) {
		var mask uint32
		for _, tok := range strings.FieldsFunc(s, func(r rune) bool {
			return r == ',' || r == ' ' || r == '\t'
		}) {
			b, e := attrBit(tok)
			if e != nil {
				return 0, e
			}
			mask |= b
		}
		return mask, nil
	}

	sc := bufio.NewScanner(r)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := sc.Text()
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "->", 2)
		if len(parts) != 2 {
			return nil, nil, fmt.Errorf("line %d: expected 'LHS -> RHS', got %q", lineNo, line)
		}
		lhs, e := parseSide(parts[0])
		if e != nil {
			return nil, nil, fmt.Errorf("line %d: %v", lineNo, e)
		}
		rhs, e := parseSide(parts[1])
		if e != nil {
			return nil, nil, fmt.Errorf("line %d: %v", lineNo, e)
		}
		fds = append(fds, FD{lhs: lhs, rhs: rhs})
	}
	if e := sc.Err(); e != nil {
		return nil, nil, e
	}
	// Reassign bit positions so that bit i corresponds to the i-th
	// attribute in lexicographic (sorted) name order.
	names = append([]string(nil), encounter...)
	sort.Strings(names)
	sortedIndex := make(map[string]uint32, len(names))
	for i, n := range names {
		sortedIndex[n] = uint32(1) << uint(i)
	}
	for i, fd := range fds {
		fds[i] = FD{
			lhs: remapBits(fd.lhs, encounter, sortedIndex),
			rhs: remapBits(fd.rhs, encounter, sortedIndex),
		}
	}
	return names, fds, nil
}

// remapBits converts a mask over encounter-order bits into a mask over
// sorted-name-order bits.
func remapBits(mask uint32, encounter []string, sortedIndex map[string]uint32) uint32 {
	var out uint32
	for m := mask; m != 0; m &= m - 1 {
		out |= sortedIndex[encounter[bits.TrailingZeros32(m&-m)]]
	}
	return out
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "keyfinder:", err)
		os.Exit(1)
	}
}

func run() error {
	var r io.Reader = os.Stdin
	if len(os.Args) > 2 {
		return fmt.Errorf("usage: keyfinder [file]")
	}
	if len(os.Args) == 2 {
		f, err := os.Open(os.Args[1])
		if err != nil {
			return err
		}
		defer f.Close()
		r = f
	}

	names, fds, err := parse(r)
	if err != nil {
		return err
	}
	n := len(names)
	var full uint32 = 1<<uint(n) - 1

	fmt.Printf("Attributes (%d): %s\n", n, strings.Join(names, ", "))
	fmt.Printf("Functional dependencies (%d):\n", len(fds))
	for _, fd := range fds {
		fmt.Printf("  %s -> %s\n", formatSet(fd.lhs, names), formatSet(fd.rhs, names))
	}

	keys := enumerateKeys(n, fds, full)
	sort.Slice(keys, func(i, j int) bool { return keyLess(keys[i], keys[j], names) })

	fmt.Printf("\nCandidate keys (%d):\n", len(keys))
	for i, k := range keys {
		label := formatSet(k, names)
		if k == 0 {
			label += " (empty key)"
		}
		fmt.Printf("\nKey %d: %s\n", i+1, label)
		fmt.Printf("  Closure: %s\n", formatSet(closure(k, fds), names))
		if k != 0 {
			fmt.Println("  Removing any member loses:")
			for m := k; m != 0; m &= m - 1 {
				bit := m & -m
				missing := full &^ closure(k&^bit, fds)
				fmt.Printf("    without %s: missing %s\n",
					names[bits.TrailingZeros32(bit)], formatSet(missing, names))
			}
		}
	}
	return nil
}
