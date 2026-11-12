package netsec

import (
	"errors"
	"testing"
)

func TestImportedValuesAreOpaqueAndUntrusted(t *testing.T) {
	ambient := []string{
		"PATH=/usr/bin:/bin",
		"EVIL=${PATH}/sneaky",
		"EMPTY=",
	}

	t.Run("opaque expansion", func(t *testing.T) {
		b := NewBuilder()
		mustDefine(t, b, Spec{Name: "WRAPPED", Template: "x${EVIL}y"})
		if err := b.Import(ambient, []string{"PATH", "EVIL", "EMPTY", "ABSENT"}); err != nil {
			t.Fatalf("Import unexpected error: %v", err)
		}
		env := mustBuild(t, b)

		evil, ok := env.Get("EVIL")
		if !ok {
			t.Fatal("EVIL not imported")
		}
		if evil.Data != "${PATH}/sneaky" {
			t.Fatalf("imported reference was interpreted: %q", evil.Data)
		}
		if len(evil.Runs) != 1 || evil.Runs[0].Origin.Source != Untrusted {
			t.Fatalf("imported value must be one untrusted run: %+v", evil.Runs)
		}

		wrapped, _ := env.Get("WRAPPED")
		if wrapped.Data != "x${PATH}/sneakyy" {
			t.Fatalf("untrusted text leaked into expansion: %q", wrapped.Data)
		}
		assertRunsCover(t, wrapped)
		firstTrusted := 1
		lastTrusted := len(wrapped.Data) - 1
		for i := range wrapped.Data {
			origin := wrapped.OriginAt(i)
			wantTrusted := i < firstTrusted || i >= lastTrusted
			if (origin.Source == Trusted) != wantTrusted {
				t.Fatalf("byte %d (%q) provenance = %s, want trusted=%v",
					i, wrapped.Data[i], origin.Source, wantTrusted)
			}
		}
		if wrapped.Runs[0].Origin != (Origin{Source: Trusted, Name: "WRAPPED"}) {
			t.Fatalf("leading run origin = %+v, want trusted WRAPPED", wrapped.Runs[0].Origin)
		}
		lastRun := wrapped.Runs[len(wrapped.Runs)-1]
		if lastRun.Origin != (Origin{Source: Trusted, Name: "WRAPPED"}) || lastRun.Data != "y" {
			t.Fatalf("trailing run = %+v, want trusted literal \"y\"", lastRun)
		}

		empty, ok := env.Get("EMPTY")
		if !ok {
			t.Fatal("EMPTY not imported")
		}
		if empty.Data != "" || len(empty.Runs) != 0 || !empty.AllTrusted() {
			t.Fatalf("empty imported value misrepresented: %+v", empty)
		}
	})

	t.Run("malformed entry", func(t *testing.T) {
		b := NewBuilder()
		err := b.Import([]string{"PATH=/bin", "BADENTRY"}, []string{"PATH"})
		var importErr *ImportError
		if !errors.As(err, &importErr) {
			t.Fatalf("want *ImportError, got %v", err)
		}
	})
}

func TestSensitiveProvenance(t *testing.T) {
	t.Run("all trusted passes", func(t *testing.T) {
		b := NewBuilder()
		mustDefine(t, b, Spec{Name: "BASE", Template: "/srv"})
		mustDefine(t, b, Spec{
			Name:      "SECRET",
			Template:  "${BASE}/key",
			Sensitive: true,
		})
		env := mustBuild(t, b)
		secret, _ := env.Get("SECRET")
		if !secret.AllTrusted() {
			t.Fatal("fully trusted sensitive value must pass")
		}
		if secret.Data != "/srv/key" {
			t.Fatalf("unexpected value %q", secret.Data)
		}
	})

	t.Run("untrusted direct byte rejected", func(t *testing.T) {
		b := NewBuilder()
		if err := b.Import([]string{"TOKEN=raw"}, []string{"TOKEN"}); err != nil {
			t.Fatal(err)
		}
		mustDefine(t, b, Spec{
			Name:      "SECRET",
			Template:  "${TOKEN}",
			Sensitive: true,
		})
		assertSensitiveFailure(t, b, "SECRET")
	})

	t.Run("untrusted through indirection rejected", func(t *testing.T) {
		b := NewBuilder()
		if err := b.Import([]string{"USER=root"}, []string{"USER"}); err != nil {
			t.Fatal(err)
		}
		mustDefine(t, b, Spec{Name: "MID", Template: "home/${USER}"})
		mustDefine(t, b, Spec{
			Name:      "SECRET",
			Template:  "/etc/${MID}/cfg",
			Sensitive: true,
		})
		assertSensitiveFailure(t, b, "SECRET")
	})

	t.Run("empty sensitive is vacuously trusted", func(t *testing.T) {
		b := NewBuilder()
		mustDefine(t, b, Spec{Name: "EMPTY", Template: "", Sensitive: true})
		mustDefine(t, b, Spec{
			Name:      "REF_EMPTY",
			Template:  "a${EMPTY}b",
			Sensitive: true,
		})
		env := mustBuild(t, b)
		ref, _ := env.Get("REF_EMPTY")
		if ref.Data != "ab" || !ref.AllTrusted() {
			t.Fatalf("empty reference mishandled: %+v", ref)
		}
	})
}

func assertSensitiveFailure(t *testing.T, b *Builder, name string) {
	t.Helper()
	_, err := b.Build()
	var sensitiveErr *SensitiveError
	if !errors.As(err, &sensitiveErr) {
		t.Fatalf("want *SensitiveError, got %v", err)
	}
	if sensitiveErr.Name != name {
		t.Fatalf("SensitiveError.Name = %q, want %q", sensitiveErr.Name, name)
	}
}

func TestMultiLevelIndirectionProvenance(t *testing.T) {
	b := NewBuilder()
	if err := b.Import([]string{"USER=root"}, []string{"USER"}); err != nil {
		t.Fatal(err)
	}
	mustDefine(t, b, Spec{Name: "L1", Template: "/home/${USER}"})
	mustDefine(t, b, Spec{Name: "L2", Template: "${L1}/app"})
	mustDefine(t, b, Spec{Name: "L3", Template: "/opt${L2}/bin"})

	env := mustBuild(t, b)
	value, ok := env.Get("L3")
	if !ok {
		t.Fatal("L3 missing")
	}
	if value.Data != "/opt/home/root/app/bin" {
		t.Fatalf("L3 = %q", value.Data)
	}
	assertRunsCover(t, value)

	for i := range value.Data {
		origin := value.OriginAt(i)
		inUntrustedSegment := i >= len("/opt/home/") && i < len("/opt/home/root")
		if inUntrustedSegment {
			if origin.Source != Untrusted || origin.Name != "USER" {
				t.Fatalf("byte %d (%q) origin = %s/%s, want untrusted USER",
					i, value.Data[i], origin.Source, origin.Name)
			}
		} else if origin.Source != Trusted {
			t.Fatalf("byte %d (%q) must be trusted, got %s/%s",
				i, value.Data[i], origin.Source, origin.Name)
		}
	}

	if env.Stats().MaxDepthObserved < 3 {
		t.Fatalf("MaxDepthObserved = %d, want at least 3", env.Stats().MaxDepthObserved)
	}
}

func TestSharedSubexpressionMemoization(t *testing.T) {
	b := NewBuilder()
	mustDefine(t, b, Spec{Name: "COMMON", Template: "/shared"})
	mustDefine(t, b, Spec{Name: "A", Template: "${COMMON}/a"})
	mustDefine(t, b, Spec{Name: "B", Template: "${COMMON}/b"})
	mustDefine(t, b, Spec{Name: "C", Template: "${A}:${B}:${COMMON}"})

	env := mustBuild(t, b)

	common, _ := env.Get("COMMON")
	a, _ := env.Get("A")
	c, _ := env.Get("C")
	if common.Data != "/shared" || a.Data != "/shared/a" {
		t.Fatalf("unexpected expansion: %q %q", common.Data, a.Data)
	}
	if c.Data != "/shared/a:/shared/b:/shared" {
		t.Fatalf("C = %q", c.Data)
	}
	assertRunsCover(t, c)

	stats := env.Stats()
	if stats.Expansions != 4 {
		t.Fatalf("Expansions = %d, want 4 distinct variables", stats.Expansions)
	}
	// COMMON is reused by A, B and C; A and B are each reused by C.
	if stats.CacheHits != 5 {
		t.Fatalf("CacheHits = %d, want 5", stats.CacheHits)
	}

	for i := range c.Data {
		if c.OriginAt(i).Source != Trusted {
			t.Fatalf("shared fragment lost trusted provenance at byte %d", i)
		}
	}
}

func TestBudgets(t *testing.T) {
	t.Run("depth budget", func(t *testing.T) {
		b := NewBuilder()
		if err := b.SetBudget(Budget{MaxOutputBytes: 1 << 16, MaxDepth: 3}); err != nil {
			t.Fatal(err)
		}
		mustDefine(t, b, Spec{Name: "L0", Template: "${L1}"})
		mustDefine(t, b, Spec{Name: "L1", Template: "${L2}"})
		mustDefine(t, b, Spec{Name: "L2", Template: "${L3}"})
		mustDefine(t, b, Spec{Name: "L3", Template: "deep"})

		_, err := b.Build()
		var budgetErr *BudgetExceededError
		if !errors.As(err, &budgetErr) || budgetErr.Kind != "depth" {
			t.Fatalf("want depth *BudgetExceededError, got %v", err)
		}
	})

	t.Run("output byte budget with shared sub-expression", func(t *testing.T) {
		b := NewBuilder()
		// COMMON costs 4 bytes once in storage but is re-copied into A and B,
		// so cumulative appended bytes count each reference.
		if err := b.SetBudget(Budget{MaxOutputBytes: 10, MaxDepth: 16}); err != nil {
			t.Fatal(err)
		}
		mustDefine(t, b, Spec{Name: "COMMON", Template: "abcd"})
		mustDefine(t, b, Spec{Name: "A", Template: "${COMMON}x"})
		mustDefine(t, b, Spec{Name: "B", Template: "${COMMON}y"})

		_, err := b.Build()
		var budgetErr *BudgetExceededError
		if !errors.As(err, &budgetErr) || budgetErr.Kind != "output bytes" {
			t.Fatalf("want output bytes *BudgetExceededError, got %v", err)
		}
	})

	t.Run("invalid budget", func(t *testing.T) {
		b := NewBuilder()
		if err := b.SetBudget(Budget{MaxOutputBytes: 0, MaxDepth: 1}); err == nil {
			t.Fatal("non-positive budget must be rejected")
		}
	})
}

func TestSharedSubexpressionRespectsBudget(t *testing.T) {
	b := NewBuilder()
	if err := b.SetBudget(Budget{MaxOutputBytes: 18, MaxDepth: 16}); err != nil {
		t.Fatal(err)
	}
	mustDefine(t, b, Spec{Name: "COMMON", Template: "abcd"})
	mustDefine(t, b, Spec{Name: "A", Template: "${COMMON}x"})
	mustDefine(t, b, Spec{Name: "B", Template: "${COMMON}y"})

	env := mustBuild(t, b)
	// Cumulative appended bytes: COMMON itself appends 4, then A appends 5
	// (4 shared + 1 literal) and B appends 5, totalling 14 even though the
	// shared fragment is stored only once.
	if got := env.Stats().OutputBytes; got != 14 {
		t.Fatalf("OutputBytes = %d, want 14", got)
	}
}
