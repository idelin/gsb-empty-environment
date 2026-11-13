package netsec

import (
	"sort"
	"strings"
	"testing"
)

type fixture struct {
	names     []string
	symoffset int
	gnu       *GNUTable
	sysv      *SysVTable
}

func newFixture(t *testing.T, pre, exports []string) fixture {
	t.Helper()

	exports = append([]string(nil), exports...)
	const gnuBuckets = uint32(4)
	const bloomShift = uint32(7)
	sort.SliceStable(exports, func(i, j int) bool {
		bi := GNUHash(exports[i]) % gnuBuckets
		bj := GNUHash(exports[j]) % gnuBuckets
		if bi != bj {
			return bi < bj
		}
		return exports[i] < exports[j]
	})

	names := []string{""}
	names = append(names, pre...)
	symoffset := len(names)
	names = append(names, exports...)

	bloom := make([]uint64, 4)
	buckets := make([]uint32, gnuBuckets)
	chains := make([]uint32, len(exports))

	for j, name := range exports {
		index := uint32(symoffset + j)
		hash := GNUHash(name)
		bucket := hash % gnuBuckets
		if buckets[bucket] == 0 {
			buckets[bucket] = index
		}

		word := (hash / 64) % uint32(len(bloom))
		bit0 := hash % 64
		bit1 := (hash >> bloomShift) % 64
		bloom[word] |= uint64(1)<<bit0 | uint64(1)<<bit1

		last := j == len(exports)-1 || GNUHash(exports[j+1])%gnuBuckets != bucket
		chains[j] = hash &^ 1
		if last {
			chains[j] |= 1
		}
	}

	sysvBuckets := make([]uint32, 8)
	next := make([]uint32, len(names))
	for i := 1; i < len(names); i++ {
		if names[i] == "" {
			continue
		}
		bucket := SysVHash(names[i]) % uint32(len(sysvBuckets))
		next[i] = sysvBuckets[bucket]
		sysvBuckets[bucket] = uint32(i)
	}

	return fixture{
		names:     names,
		symoffset: symoffset,
		gnu: &GNUTable{
			NBuckets:   gnuBuckets,
			SymOffset:  uint32(symoffset),
			Bloom:      bloom,
			BloomShift: bloomShift,
			Buckets:    buckets,
			Chains:     chains,
		},
		sysv: &SysVTable{Buckets: sysvBuckets, Next: next},
	}
}

func (f fixture) clone() fixture {
	gnu := *f.gnu
	sysv := *f.sysv
	clone := fixture{
		names:     append([]string(nil), f.names...),
		symoffset: f.symoffset,
		gnu:       &gnu,
		sysv:      &sysv,
	}
	clone.gnu.Bloom = append([]uint64(nil), f.gnu.Bloom...)
	clone.gnu.Buckets = append([]uint32(nil), f.gnu.Buckets...)
	clone.gnu.Chains = append([]uint32(nil), f.gnu.Chains...)
	clone.sysv.Buckets = append([]uint32(nil), f.sysv.Buckets...)
	clone.sysv.Next = append([]uint32(nil), f.sysv.Next...)
	return clone
}

func namesFor(exports ...string) (pre, gnu []string) {
	return []string{"legacy_init", "legacy_fini"}, exports
}

func defaultFixture(t *testing.T) fixture {
	t.Helper()
	pre, exports := namesFor("alpha", "beta", "gamma", "delta", "epsilon", "zeta")
	return newFixture(t, pre, exports)
}

func findCheck(report *Report, name string) *SymbolCheck {
	for i := range report.Checks {
		if report.Checks[i].Name == name {
			return &report.Checks[i]
		}
	}
	return nil
}

func findIssue(report *Report, code IssueCode, name string) *Issue {
	for i := range report.Issues {
		if report.Issues[i].Code == code && report.Issues[i].Name == name {
			return &report.Issues[i]
		}
	}
	return nil
}

func symbolIndex(t *testing.T, names []string, name string) uint32 {
	t.Helper()
	index, ok := LinearLookup(names, name)
	if !ok {
		t.Fatalf("symbol %q not present", name)
	}
	return index
}

func TestHashVectors(t *testing.T) {
	vectors := []struct {
		name string
		gnu  uint32
		sysv uint32
	}{
		{"", 0x00001505, 0x00000000},
		{"a", 0x0002b606, 0x00000061},
		{"alpha", 0x0f176c2b, 0x006836e1},
		{"ghost", 0x0f81caea, 0x006df6a4},
	}
	for _, vector := range vectors {
		if got := GNUHash(vector.name); got != vector.gnu {
			t.Fatalf("GNUHash(%q) = 0x%08x, want 0x%08x", vector.name, got, vector.gnu)
		}
		if got := SysVHash(vector.name); got != vector.sysv {
			t.Fatalf("SysVHash(%q) = 0x%08x, want 0x%08x", vector.name, got, vector.sysv)
		}
	}
}

func TestValidDualIndex(t *testing.T) {
	f := defaultFixture(t)
	report := Verify(f.names, f.gnu, f.sysv)
	if !report.OK {
		t.Fatalf("valid fixture rejected: %v", report.Err())
	}
	if got, want := len(report.Checks), len(f.names)-1; got != want {
		t.Fatalf("got %d symbol checks, want %d", got, want)
	}
	for _, check := range report.Checks {
		if check.SysV == nil || check.SysV.Err != nil || !check.SysV.Found || check.SysV.Index != check.Index {
			t.Fatalf("bad System V check for %s", check)
		}
		if check.Index >= uint32(f.symoffset) {
			if check.GNU == nil || check.GNU.Err != nil || !check.GNU.Found || check.GNU.Index != check.Index {
				t.Fatalf("bad GNU check for %s", check)
			}
		}
	}
	t.Logf("valid export paths: %s", findCheck(report, "delta"))
}

func TestBloomCorruptionHidesNamedExport(t *testing.T) {
	f := defaultFixture(t).clone()
	const victim = "gamma"
	hash := GNUHash(victim)
	word := (hash / 64) % uint32(len(f.gnu.Bloom))
	mask := uint64(1)<<(hash%64) | uint64(1)<<((hash>>f.gnu.BloomShift)%64)
	f.gnu.Bloom[word] &^= mask

	report := Verify(f.names, f.gnu, f.sysv)
	if report.OK {
		t.Fatal("cleared Bloom bits were accepted")
	}
	issue := findIssue(report, CodeMissingExport, victim)
	if issue == nil {
		t.Fatalf("no missing-export issue for %q: %v", victim, report.Err())
	}
	if issue.GNU == nil || issue.GNU.BloomHit {
		t.Fatalf("expected GNU Bloom miss path, got %v", issue.GNU)
	}
	if issue.SysV == nil || !issue.SysV.Found || issue.SysV.MatchIndex != symbolIndex(t, f.names, victim) {
		t.Fatalf("expected intact System V path, got %v", issue.SysV)
	}
	check := findCheck(report, victim)
	t.Logf("bloom corruption victim %q: %s; %s", victim, check.GNU, check.SysV)
}

func TestBloomFalsePositiveIsNotAnAttack(t *testing.T) {
	f := defaultFixture(t).clone()
	const absent = "ghost"
	hash := GNUHash(absent)
	word := (hash / 64) % uint32(len(f.gnu.Bloom))
	mask := uint64(1)<<(hash%64) | uint64(1)<<((hash>>f.gnu.BloomShift)%64)
	f.gnu.Bloom[word] |= mask

	report := Verify(f.names, f.gnu, f.sysv)
	if !report.OK {
		t.Fatalf("Bloom false positive was treated as an attack: %v", report.Err())
	}
	check := CheckName(f.names, absent, f.gnu, f.sysv)
	if check.LinearFound {
		t.Fatalf("%q unexpectedly present", absent)
	}
	if check.GNU.GNU == nil || !check.GNU.GNU.BloomHit {
		t.Fatalf("fixture did not produce the intended Bloom hit: %s", check.GNU)
	}
	if check.GNU.Found || check.SysV.Found {
		t.Fatalf("absent name was found: %s", check)
	}
	t.Logf("Bloom false positive is benign: %s", check)
}

func TestTerminatorBitCorruption(t *testing.T) {
	f := defaultFixture(t).clone()
	const corruptedBucket = uint32(1)
	var last int = -1
	for j := 0; j < len(f.gnu.Chains); j++ {
		i := f.symoffset + j
		if GNUHash(f.names[i])%f.gnu.NBuckets != corruptedBucket {
			continue
		}
		last = j
		if f.gnu.Chains[j]&1 != 0 {
			break
		}
	}
	if last < 0 {
		t.Fatal("fixture has no nonempty GNU bucket")
	}
	f.gnu.Chains[last] &^= 1

	report := Verify(f.names, f.gnu, f.sysv)
	if report.OK {
		t.Fatal("missing GNU termination bit was accepted")
	}
	var mismatch *Issue
	for i := range report.Issues {
		if report.Issues[i].Code == CodeChainMismatch {
			mismatch = &report.Issues[i]
			break
		}
	}
	if mismatch == nil {
		t.Fatalf("no chain-mismatch issue after clearing termination bit: %v", report.Err())
	}
	if mismatch.GNU == nil || mismatch.SysV == nil {
		t.Fatalf("terminator issue lacks both lookup paths: %s", mismatch.Error())
	}
	check := findCheck(report, mismatch.Name)
	t.Logf("terminator corruption first affects %q: %s; %s", mismatch.Name, check.GNU, check.SysV)
	t.Logf("terminator issue: %s", mismatch.Error())
	if !strings.Contains(mismatch.Detail, "GNU hash bucket") {
		t.Fatalf("unexpected terminator issue: %s", mismatch.Error())
	}
}

func TestBucketCorruptionHidesBucket(t *testing.T) {
	f := defaultFixture(t).clone()
	const hiddenBucket = uint32(3)
	if f.gnu.Buckets[hiddenBucket] == 0 {
		t.Fatal("fixture bucket is empty")
	}
	f.gnu.Buckets[hiddenBucket] = 0

	report := Verify(f.names, f.gnu, f.sysv)
	if report.OK {
		t.Fatal("zeroed GNU bucket was accepted")
	}
	var victim string
	for i := f.symoffset; i < len(f.names); i++ {
		if GNUHash(f.names[i])%f.gnu.NBuckets == hiddenBucket {
			victim = f.names[i]
			break
		}
	}
	issue := findIssue(report, CodeMissingExport, victim)
	if issue == nil {
		t.Fatalf("no missing-export issue for bucket victim %q: %v", victim, report.Err())
	}
	if issue.GNU == nil || !issue.GNU.BloomHit || !issue.GNU.Terminated || issue.GNU.Start != 0 {
		t.Fatalf("expected GNU empty-bucket path, got %v", issue.GNU)
	}
	if issue.SysV == nil || !issue.SysV.Found || issue.SysV.MatchIndex != symbolIndex(t, f.names, victim) {
		t.Fatalf("expected intact System V path, got %v", issue.SysV)
	}
	check := findCheck(report, victim)
	t.Logf("bucket corruption victim %q: %s; %s", victim, check.GNU, check.SysV)
}

func TestNextCorruptionCycleAndHiding(t *testing.T) {
	t.Run("cycle", func(t *testing.T) {
		f := defaultFixture(t).clone()
		bucket, chain := longestSysVBucket(f.sysv)
		if len(chain) < 2 {
			t.Fatal("fixture has no multi-node System V chain")
		}
		loopAt := chain[0]
		f.sysv.Next[loopAt] = loopAt

		report := Verify(f.names, f.gnu, f.sysv)
		if report.OK {
			t.Fatal("System V cycle was accepted")
		}
		issue := findIssue(report, CodeChainCycle, f.names[loopAt])
		if issue == nil {
			t.Fatalf("no cycle issue for %q in bucket %d: %v", f.names[loopAt], bucket, report.Err())
		}
		check := findCheck(report, f.names[loopAt])
		t.Logf("next cycle in bucket %d at %q: %s; %s", bucket, f.names[loopAt], check.GNU, check.SysV)
	})

	t.Run("hides successor", func(t *testing.T) {
		f := defaultFixture(t).clone()
		bucket, chain := longestSysVBucket(f.sysv)
		if len(chain) < 2 {
			t.Fatal("fixture has no multi-node System V chain")
		}
		first := chain[0]
		hidden := chain[1]
		f.sysv.Next[first] = 0

		report := Verify(f.names, f.gnu, f.sysv)
		if report.OK {
			t.Fatal("truncated System V chain was accepted")
		}
		issue := findIssue(report, CodeMissingExport, f.names[hidden])
		if issue == nil {
			t.Fatalf("no hidden export issue for %q in bucket %d: %v", f.names[hidden], bucket, report.Err())
		}
		if issue.SysV == nil || issue.SysV.Found {
			t.Fatalf("expected failed System V path, got %v", issue.SysV)
		}
		if issue.GNU == nil || !issue.GNU.Found {
			t.Fatalf("expected intact GNU path, got %v", issue.GNU)
		}
		check := findCheck(report, f.names[hidden])
		t.Logf("next corruption hides %q: %s; %s", f.names[hidden], check.GNU, check.SysV)
	})
}

func longestSysVBucket(table *SysVTable) (uint32, []uint32) {
	var bestBucket uint32
	var best []uint32
	for bucket := uint32(0); bucket < uint32(len(table.Buckets)); bucket++ {
		var chain []uint32
		seen := make(map[uint32]struct{})
		for i := table.Buckets[bucket]; i != 0; i = table.Next[i] {
			if _, ok := seen[i]; ok {
				break
			}
			seen[i] = struct{}{}
			chain = append(chain, i)
		}
		if len(chain) > len(best) {
			bestBucket, best = bucket, chain
		}
	}
	return bestBucket, best
}

func TestMalformedTablesAndWorkBudget(t *testing.T) {
	base := defaultFixture(t)

	tests := []struct {
		name   string
		mutate func(*fixture)
		option []Option
		code   IssueCode
	}{
		{
			name:   "short gnu buckets",
			mutate: func(f *fixture) { f.gnu.Buckets = f.gnu.Buckets[:f.gnu.NBuckets-1] },
			code:   CodeMalformedTable,
		},
		{
			name:   "extra gnu bucket",
			mutate: func(f *fixture) { f.gnu.Buckets = append(f.gnu.Buckets, 0) },
			code:   CodeMalformedTable,
		},
		{
			name: "non-power-of-two bloom",
			mutate: func(f *fixture) {
				f.gnu.Bloom = append(f.gnu.Bloom, 0)
			},
			code: CodeMalformedTable,
		},
		{
			name:   "bloom shift out of range",
			mutate: func(f *fixture) { f.gnu.BloomShift = 32 },
			code:   CodeMalformedTable,
		},
		{
			name:   "symoffset past table",
			mutate: func(f *fixture) { f.gnu.SymOffset = uint32(len(f.names) + 1) },
			code:   CodeMalformedTable,
		},
		{
			name:   "short chains",
			mutate: func(f *fixture) { f.gnu.Chains = f.gnu.Chains[:len(f.gnu.Chains)-1] },
			code:   CodeMalformedTable,
		},
		{
			name:   "extra chains",
			mutate: func(f *fixture) { f.gnu.Chains = append(f.gnu.Chains, 1) },
			code:   CodeMalformedTable,
		},
		{
			name:   "short sysv next",
			mutate: func(f *fixture) { f.sysv.Next = f.sysv.Next[:len(f.sysv.Next)-1] },
			code:   CodeMalformedTable,
		},
		{
			name:   "extra sysv next",
			mutate: func(f *fixture) { f.sysv.Next = append(f.sysv.Next, 0) },
			code:   CodeMalformedTable,
		},
		{
			name:   "no sysv buckets",
			mutate: func(f *fixture) { f.sysv.Buckets = nil },
			code:   CodeMalformedTable,
		},
		{
			name:   "bucket index out of range",
			mutate: func(f *fixture) { f.gnu.Buckets[1] = uint32(len(f.names)) },
			code:   CodeBadIndex,
		},
		{
			name:   "work budget",
			mutate: func(f *fixture) {},
			option: []Option{WithWorkBudget(1)},
			code:   CodeWorkBudget,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := base.clone()
			test.mutate(&f)
			report := Verify(f.names, f.gnu, f.sysv, test.option...)
			if report.OK {
				t.Fatal("malformed input accepted")
			}
			found := false
			for _, issue := range report.Issues {
				if issue.Code == test.code {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("no %s issue: %v", test.code, report.Err())
			}
		})
	}
}

func TestNameZeroAndDuplicateNamesRejected(t *testing.T) {
	f := defaultFixture(t).clone()
	f.names[0] = "not-empty"
	report := Verify(f.names, f.gnu, f.sysv)
	if report.OK {
		t.Fatal("nonempty symbol zero accepted")
	}
	var symbolZero *Issue
	for i := range report.Issues {
		if report.Issues[i].Code == CodeBadNameTable {
			symbolZero = &report.Issues[i]
			break
		}
	}
	if symbolZero == nil {
		t.Fatalf("no symbol-zero issue: %v", report.Err())
	}

	f = defaultFixture(t).clone()
	f.names[4] = f.names[3]
	report = Verify(f.names, f.gnu, f.sysv)
	if report.OK {
		t.Fatal("duplicate name accepted")
	}
	if issue := findIssue(report, CodeDuplicateName, f.names[4]); issue == nil {
		t.Fatalf("no duplicate-name issue: %v", report.Err())
	}
}

func TestFixtureNamesAndPathsAreReadable(t *testing.T) {
	f := defaultFixture(t)
	for _, name := range []string{"legacy_init", "delta", "zeta"} {
		check := CheckName(f.names, name, f.gnu, f.sysv)
		if !strings.Contains(check.String(), "gnu(hash=") {
			t.Fatalf("GNU path missing for %q: %s", name, check)
		}
		if !strings.Contains(check.String(), "sysv(hash=") {
			t.Fatalf("System V path missing for %q: %s", name, check)
		}
		t.Logf("%s", check)
	}
}
