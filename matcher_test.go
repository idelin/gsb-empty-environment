package netsec

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestSupportedSubsetSemantics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		pattern string
		input   string
		want    bool
	}{
		{`abc`, `xxabcxx`, true},
		{`abc`, `abx`, false},
		{`^abc$`, `abc`, true},
		{`^abc$`, `xabc`, false},
		{`^abc`, `abcdef`, true},
		{`abc$`, `xxabc`, true},
		{`a*b+c?`, `bb`, true},
		{`a*b+c?`, `ac`, false},
		{`(cat|dog)+$`, `catdogcat`, true},
		{`(cat|dog)+$`, `catdogs`, false},
		{`[a-z]+[0-9]?`, `hello7`, true},
		{`[^0-9]+`, `abc`, true},
		{`[^0-9]+`, `123`, false},
		{`\d+\w\s?`, `42_x`, true},
		{`[\]\-]+`, `]-`, true},
		{`a\.b`, `a.b`, true},
		{`a\.b`, `axb`, false},
		{`(ab)?c`, `c`, true},
		{`(ab)?c`, `abc`, true},
		{`.+`, "\n", false},
		{`.+`, "x", true},
		{`^$`, ``, true},
		{`^$`, `x`, false},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.pattern+"/"+tc.input, func(t *testing.T) {
			m, err := Compile(tc.pattern)
			if err != nil {
				t.Fatalf("Compile(%q): %v", tc.pattern, err)
			}
			got := m.Match(tc.input, 1<<20)
			if got.Exhausted {
				t.Fatalf("unexpected budget exhaustion after %d steps", got.Steps)
			}
			if got.Matched != tc.want {
				t.Fatalf("Match(%q) = %+v, want matched=%v", tc.input, got, tc.want)
			}
			if got.Allowed == got.Blocked {
				t.Fatalf("inconsistent verdict: %+v", got)
			}
		})
	}
}

func TestRejectsBackreferencesAndUnsupportedSyntax(t *testing.T) {
	t.Parallel()

	patterns := []string{
		`(a)\1`,
		`(a)\2`,
		`\k<name>`,
		`(a)(b)\1\2`,
		`a{3}`,
		`(?=a)`,
		`(?:a)`,
		`(a`,
		`[a`,
		`[z-a]`,
		`a**`,
		`^*`,
		`\`,
		`\q`,
	}

	for _, pattern := range patterns {
		pattern := pattern
		t.Run(pattern, func(t *testing.T) {
			if _, err := Compile(pattern); err == nil {
				t.Fatalf("Compile(%q) unexpectedly succeeded", pattern)
			}
		})
	}
}

func TestDifferentialAgainstRegexp(t *testing.T) {
	t.Parallel()

	patterns := []string{
		``,
		`a`,
		`ab`,
		`a|b`,
		`(a|b)c`,
		`a*`,
		`a+`,
		`a?`,
		`(ab)*`,
		`(a+)+`,
		`^a`,
		`a$`,
		`^a$`,
		`[ab]`,
		`[^ab]`,
		`[a-c]`,
		`\d`,
		`\w`,
		`\s`,
		`.`,
	}
	inputs := []string{
		``,
		`a`,
		`b`,
		`c`,
		`ab`,
		`abc`,
		`aaa`,
		`aab`,
		`!`,
		`0`,
		`_`,
		"\n",
		`abab`,
		`xax`,
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		m := MustCompile(pattern)
		for _, input := range inputs {
			want := re.MatchString(input)
			got := m.Match(input, 1<<20)
			if got.Exhausted {
				t.Fatalf("pattern %q input %q exhausted after %d steps", pattern, input, got.Steps)
			}
			if got.Matched != want {
				t.Fatalf(
					"pattern %q input %q: netsec matched=%v, regexp matched=%v",
					pattern, input, got.Matched, want,
				)
			}
		}
	}
}

func TestBudgetBoundaryIsFailClosed(t *testing.T) {
	t.Parallel()

	m := MustCompile(`(a+)+$`)
	input := strings.Repeat("a", 4096) + "!"

	reference := m.Match(input, 1<<30)
	if reference.Exhausted || reference.Matched {
		t.Fatalf("reference run = %+v, want definitive no-match", reference)
	}

	exact := m.Match(input, reference.Steps)
	if exact.Exhausted {
		t.Fatalf("budget equal to used steps (%d) unexpectedly exhausted", exact.Steps)
	}
	if exact.Matched || exact.Steps != reference.Steps {
		t.Fatalf("exact-budget run = %+v, want no-match with %d steps", exact, reference.Steps)
	}

	short := m.Match(input, reference.Steps-1)
	if !short.Exhausted || !short.FailClosed || !short.Blocked || short.Allowed {
		t.Fatalf("short-budget run is not fail-closed: %+v", short)
	}
	if short.Steps != short.Budget {
		t.Fatalf("short-budget used %d steps, budget %d", short.Steps, short.Budget)
	}

	zero := m.Match(input, 0)
	if !zero.Exhausted || !zero.FailClosed || !zero.Blocked || zero.Steps != 0 {
		t.Fatalf("zero-budget run = %+v", zero)
	}
}

func TestCatastrophicInputStaysBounded(t *testing.T) {
	t.Parallel()

	const size = 64 << 10
	m := MustCompile(`(a+)+$`)
	malicious := strings.Repeat("a", size) + "!"
	normal := strings.Repeat("a", size)

	start := time.Now()
	blocked := m.Match(malicious, 1<<20)
	elapsed := time.Since(start)

	if blocked.Exhausted {
		t.Fatalf("64KB malicious input exhausted a 1Mi-step budget after %d steps", blocked.Steps)
	}
	if blocked.Matched || !blocked.Allowed || blocked.Steps == 0 {
		t.Fatalf("64KB malicious input result = %+v, want definitive no-match", blocked)
	}
	t.Logf("64KB malicious input: %d steps in %v", blocked.Steps, elapsed)
	// This is a generous wall-clock guard; the linear NFA should finish in
	// microseconds to low milliseconds on ordinary CI machines.
	if elapsed > 2*time.Second {
		t.Fatalf("64KB malicious match took %v", elapsed)
	}

	normalResult := m.Match(normal, 1<<20)
	if normalResult.Exhausted || !normalResult.Matched || !normalResult.Blocked {
		t.Fatalf("64KB normal input result = %+v", normalResult)
	}
}

func TestWorkGrowsLinearlyWithInput(t *testing.T) {
	t.Parallel()

	m := MustCompile(`(a+)+$`)
	lastSteps := 0
	lastDelta := 0

	for size := 4 << 10; size <= 64<<10; size *= 2 {
		input := strings.Repeat("a", size) + "!"
		result := m.Match(input, 1<<24)
		if result.Exhausted || result.Matched {
			t.Fatalf("size %d result = %+v, want definitive no-match", size, result)
		}

		delta := result.Steps - lastSteps
		if lastSteps > 0 && delta > 2*lastDelta+128 {
			t.Fatalf(
				"step growth is not linear: size=%d steps=%d delta=%d previous-delta=%d",
				size, result.Steps, delta, lastDelta,
			)
		}
		lastSteps = result.Steps
		lastDelta = delta
	}
}

func BenchmarkCatastrophic64KB(b *testing.B) {
	input := strings.Repeat("a", 64<<10) + "!"
	m := MustCompile(`(a+)+$`)
	b.ReportAllocs()
	b.SetBytes(int64(len(input)))
	for b.Loop() {
		_ = m.Match(input, 1<<20)
	}
}
