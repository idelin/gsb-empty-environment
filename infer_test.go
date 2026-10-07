package infer

import (
	"math/big"
	"math/rand"
	"testing"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		value string
		want  Kind
	}{
		// Booleans.
		{"true", Bool},
		{"false", Bool},
		{"True", Text},
		{"TRUE", Text},
		{"1", Int}, // not a bool

		// Integers, including huge ones beyond uint64.
		{"0", Int},
		{"7", Int},
		{"-42", Int},
		{"9223372036854775807", Int},
		{"18446744073709551615", Int},
		{"99999999999999999999999999999999999999999999", Int},
		{"-99999999999999999999999999999999999999999999", Int},

		// Leading zeros and negated zero are text: the canonical
		// numeric form would not round-trip to the original string.
		{"00", Text},
		{"007", Text},
		{"-007", Text},
		{"-0", Text},
		{"01.5", Text},
		{"00.5", Text},
		{"-0.0", Text},
		{"-0/3", Text},

		// Rationals.
		{"0.5", Rational},
		{"-0.5", Rational},
		{"3.14159", Rational},
		{"-2.75", Rational},
		{"1.50", Rational}, // trailing zero: still exactly representable
		{"22/7", Rational},
		{"-22/7", Rational},
		{"0/1", Rational},
		{"1/0", Text},   // zero denominator
		{"1/007", Text}, // leading zero in denominator
		{"007/1", Text}, // leading zero in numerator
		{".5", Text},    // missing integer part
		{"5.", Text},    // missing fractional part
		{"1e3", Text},   // exponent form unsupported
		{"1.2.3", Text},

		// Text, including the empty string (text, not null).
		{"", Text},
		{"hello", Text},
		{" 1", Text},
		{"1 ", Text},
		{"--1", Text},
		{"-", Text},
		{"1/2/3", Text},
	}
	for _, c := range cases {
		if got := Classify(c.value); got != c.want {
			t.Errorf("Classify(%q) = %s, want %s", c.value, got, c.want)
		}
	}
}

// TestHugeIntegersAreInt confirms that integers far beyond int64 and
// uint64 range are still inferred as Int, and that big.Int agrees they
// are well-formed integers.
func TestHugeIntegersAreInt(t *testing.T) {
	huge := new(big.Int).Exp(big.NewInt(10), big.NewInt(100), nil) // 10^100
	for _, s := range []string{huge.String(), "-" + huge.String()} {
		if got := Classify(s); got != Int {
			t.Errorf("Classify(%q) = %s, want int", s, got)
		}
		if _, ok := new(big.Int).SetString(s, 10); !ok {
			t.Errorf("big.Int rejected %q", s)
		}
	}
}

// TestEmptyColumn verifies the defaults: a column with no values is
// Bool (the narrowest kind, vacuously), while a column containing only
// empty strings is Text because "" is a text value, not a null.
func TestEmptyColumn(t *testing.T) {
	c := NewColumn()
	if got := c.Kind(); got != Bool {
		t.Errorf("empty column Kind() = %s, want bool", got)
	}
	if c.Len() != 0 {
		t.Errorf("empty column Len() = %d, want 0", c.Len())
	}

	c.Add("")
	if got := c.Kind(); got != Text {
		t.Errorf("column of empty strings Kind() = %s, want text", got)
	}
	if err := c.Remove(""); err != nil {
		t.Fatalf("Remove(\"\") failed: %v", err)
	}
	if got := c.Kind(); got != Bool {
		t.Errorf("after removing the only value Kind() = %s, want bool", got)
	}
}

// TestRemoveNarrowsType checks that retracting the widest values
// narrows the inferred type step by step, back down to the default.
func TestRemoveNarrowsType(t *testing.T) {
	c := NewColumn()
	for _, v := range []string{"true", "7", "0.5", "hello"} {
		c.Add(v)
	}
	steps := []struct {
		remove string
		want   Kind
	}{
		{"hello", Rational},
		{"0.5", Int},
		{"7", Bool},
		{"true", Bool}, // empty column defaults to bool
	}
	for _, s := range steps {
		if err := c.Remove(s.remove); err != nil {
			t.Fatalf("Remove(%q) failed: %v", s.remove, err)
		}
		if got := c.Kind(); got != s.want {
			t.Errorf("after removing %q, Kind() = %s, want %s", s.remove, got, s.want)
		}
	}
	if c.Len() != 0 {
		t.Errorf("Len() = %d, want 0", c.Len())
	}
}

func TestRemoveAbsentValueFails(t *testing.T) {
	c := NewColumn()
	c.Add("true")
	if err := c.Remove("hello"); err == nil {
		t.Error("Remove of absent text value succeeded, want error")
	}
	if err := c.Remove("true"); err != nil {
		t.Errorf("Remove of live value failed: %v", err)
	}
	if err := c.Remove("true"); err == nil {
		t.Error("second Remove of same value succeeded, want error")
	}
}

// fullScanKind recomputes a column's kind from scratch by classifying
// every live value, with no incremental state.
func fullScanKind(values []string) Kind {
	kind := Bool
	for _, v := range values {
		if k := Classify(v); k > kind {
			kind = k
		}
	}
	return kind
}

func fullScanCounts(values []string) map[Kind]int {
	counts := map[Kind]int{Bool: 0, Int: 0, Rational: 0, Text: 0}
	for _, v := range values {
		counts[Classify(v)]++
	}
	return counts
}

// TestIncrementalMatchesFullScan cross-checks the incremental
// add/remove bookkeeping against a full scan of the live values after
// every operation, using a random workload that includes leading-zero
// strings, huge integers, empty strings, and empty columns.
func TestIncrementalMatchesFullScan(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	pool := []string{
		"true", "false",
		"0", "1", "-17", "42",
		"99999999999999999999999999999999999999999999",
		"007", "-0", "01.5",
		"0.5", "-3.25", "22/7", "1.50",
		"", "hello", "not a number", "1e3",
	}

	const cols = 4
	table := NewTable(cols)
	live := make([][]string, cols) // live values per column

	check := func(step int) {
		for i := 0; i < cols; i++ {
			want := fullScanKind(live[i])
			if got := table.Kind(i); got != want {
				t.Fatalf("step %d, column %d: incremental Kind() = %s, full scan = %s", step, i, got, want)
			}
			wantCounts := fullScanCounts(live[i])
			if got := table.Column(i).Counts(); !countsEqual(got, wantCounts) {
				t.Fatalf("step %d, column %d: incremental Counts() = %v, full scan = %v", step, i, got, wantCounts)
			}
			if got := table.Column(i).Len(); got != len(live[i]) {
				t.Fatalf("step %d, column %d: Len() = %d, want %d", step, i, got, len(live[i]))
			}
		}
	}

	for step := 0; step < 2000; step++ {
		row := make([]string, cols)
		if rng.Intn(3) == 0 {
			// Remove a random live value per column, if any.
			for i := range row {
				if len(live[i]) == 0 {
					row[i] = pool[rng.Intn(len(pool))] // never added; skip removal below
					continue
				}
				row[i] = live[i][rng.Intn(len(live[i]))]
			}
			for i, v := range row {
				idx := indexOf(live[i], v)
				if idx < 0 {
					continue
				}
				if err := table.Column(i).Remove(v); err != nil {
					t.Fatalf("step %d, column %d: Remove(%q) failed: %v", step, i, v, err)
				}
				live[i] = append(live[i][:idx], live[i][idx+1:]...)
			}
		} else {
			for i := range row {
				row[i] = pool[rng.Intn(len(pool))]
			}
			if err := table.AddRow(row); err != nil {
				t.Fatalf("step %d: AddRow(%v) failed: %v", step, row, err)
			}
			for i, v := range row {
				live[i] = append(live[i], v)
			}
		}
		check(step)
	}
}

func countsEqual(a, b map[Kind]int) bool {
	for _, k := range []Kind{Bool, Int, Rational, Text} {
		if a[k] != b[k] {
			return false
		}
	}
	return true
}

func indexOf(values []string, target string) int {
	for i, v := range values {
		if v == target {
			return i
		}
	}
	return -1
}

func TestTableSchema(t *testing.T) {
	table := NewTable(3)
	rows := [][]string{
		{"true", "5", "x"},
		{"false", "-9", ""},
	}
	for _, r := range rows {
		if err := table.AddRow(r); err != nil {
			t.Fatalf("AddRow(%v) failed: %v", r, err)
		}
	}
	want := []Kind{Bool, Int, Text}
	got := table.Schema()
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("column %d: Schema() = %s, want %s", i, got[i], want[i])
		}
	}

	if err := table.AddRow([]string{"true", "5"}); err == nil {
		t.Error("AddRow with wrong width succeeded, want error")
	}
	if err := table.RemoveRow(rows[1]); err != nil {
		t.Fatalf("RemoveRow failed: %v", err)
	}
	if got := table.Kind(2); got != Text {
		t.Errorf("after removing the empty string, column 2 Kind() = %s, want text (still has \"x\")", got)
	}
	if err := table.RemoveRow(rows[0]); err != nil {
		t.Fatalf("RemoveRow failed: %v", err)
	}
	for i := 0; i < 3; i++ {
		if got := table.Kind(i); got != Bool {
			t.Errorf("empty table column %d Kind() = %s, want bool", i, got)
		}
	}
}
