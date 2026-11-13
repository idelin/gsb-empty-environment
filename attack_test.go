package netsec

import (
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"testing"
)

func rawOracle(n, d *big.Int) ParityOracleFunc {
	return func(x *big.Int) (OracleResult, error) {
		if err := CheckQuery(n, x); err != nil {
			return ResultRejected, err
		}
		y := new(big.Int).Exp(x, d, n)
		if y.Bit(0) == 1 {
			return ResultOdd, nil
		}
		return ResultEven, nil
	}
}

func modInverseOrNil(a, m int64) *big.Int {
	return new(big.Int).ModInverse(big.NewInt(a), big.NewInt(m))
}

func TestExhaustiveSmallModuli(t *testing.T) {
	moduli := []struct {
		n   int64
		phi int64
	}{
		{15, 8}, {21, 12}, {33, 20}, {35, 24}, {39, 24}, {51, 32},
		{55, 40}, {57, 36}, {65, 48}, {77, 60}, {91, 72}, {95, 72},
	}

	for _, mod := range moduli {
		n := big.NewInt(mod.n)
		for _, eInt := range []int64{3, 5, 7, 11, 13} {
			e := big.NewInt(eInt)
			if new(big.Int).GCD(nil, nil, e, big.NewInt(mod.phi)).Cmp(big.NewInt(1)) != 0 {
				continue
			}
			d := modInverseOrNil(eInt, mod.phi)
			if d == nil {
				t.Fatalf("inverse missing for N=%d e=%d", mod.n, eInt)
			}
			oracle := rawOracle(n, d)

			for mInt := int64(0); mInt < mod.n; mInt++ {
				m := big.NewInt(mInt)
				c := new(big.Int).Exp(m, e, n)

				// The logger independently checks every exact endpoint,
				// query, and parity observation.
				ratLo := new(big.Rat)
				ratHi := new(big.Rat).SetInt(n)
				seenStep := 0
				logger := func(ev LogEvent) {
					if ev.Result != "even" && ev.Result != "odd" {
						t.Fatalf("N=%d e=%d m=%d: unexpected result %q", mod.n, eInt, mInt, ev.Result)
					}
					seenStep++
					if ev.Step != seenStep {
						t.Fatalf("N=%d e=%d m=%d: step=%d want %d", mod.n, eInt, mInt, ev.Step, seenStep)
					}
					mid := new(big.Rat).Add(ratLo, ratHi)
					mid.Quo(mid, big.NewRat(2, 1))
					if ev.Result == "even" {
						ratHi = mid
					} else {
						ratLo = mid
					}

					gotLo, err := parseLoggedRat(ev.Lower)
					if err != nil {
						t.Fatalf("N=%d e=%d m=%d: lower: %v", mod.n, eInt, mInt, err)
					}
					gotHi, err := parseLoggedRat(ev.Upper)
					if err != nil {
						t.Fatalf("N=%d e=%d m=%d: upper: %v", mod.n, eInt, mInt, err)
					}
					if gotLo.Cmp(ratLo) != 0 || gotHi.Cmp(ratHi) != 0 {
						t.Fatalf("N=%d e=%d m=%d step %d: endpoints got [%s,%s] want [%s,%s]",
							mod.n, eInt, mInt, seenStep, gotLo.RatString(), gotHi.RatString(),
							ratLo.RatString(), ratHi.RatString())
					}
					mRat := new(big.Rat).SetInt(m)
					if mRat.Cmp(ratLo) < 0 || mRat.Cmp(ratHi) > 0 {
						t.Fatalf("N=%d e=%d m=%d: m escaped interval at step %d", mod.n, eInt, mInt, seenStep)
					}

					scaled := new(big.Int).Lsh(new(big.Int).Set(m), uint(ev.Step))
					scaled.Mod(scaled, n)
					want := "even"
					if scaled.Bit(0) == 1 {
						want = "odd"
					}
					if ev.Result != want {
						t.Fatalf("N=%d e=%d m=%d step %d: parity=%s want %s",
							mod.n, eInt, mInt, ev.Step, ev.Result, want)
					}

					exp := new(big.Int).Mul(e, big.NewInt(int64(ev.Step)))
					q := new(big.Int).Exp(big.NewInt(2), exp, n)
					q.Mul(q, c)
					q.Mod(q, n)
					if ev.Query != q.Text(16) {
						t.Fatalf("N=%d e=%d m=%d step %d: query=%s want %s",
							mod.n, eInt, mInt, ev.Step, ev.Query, q.Text(16))
					}
				}

				s, err := NewSession(Config{
					N:          n,
					E:          e,
					C:          c,
					MaxQueries: n.BitLen() + 2,
					Logger:     logger,
				})
				if err != nil {
					t.Fatalf("N=%d e=%d m=%d: NewSession: %v", mod.n, eInt, mInt, err)
				}
				out, err := s.Run(oracle)
				if err != nil {
					t.Fatalf("N=%d e=%d m=%d: Run: %v", mod.n, eInt, mInt, err)
				}
				if !out.Succeeded() {
					t.Fatalf("N=%d e=%d m=%d: status=%s queries=%d", mod.n, eInt, mInt, out.Status, out.QueriesUsed)
				}
				if out.Recovered.Cmp(m) != 0 {
					t.Fatalf("N=%d e=%d m=%d: recovered %s", mod.n, eInt, mInt, out.Recovered)
				}
				if !out.Verified {
					t.Fatalf("N=%d e=%d m=%d: success without verification", mod.n, eInt, mInt)
				}
				if out.QueriesUsed > n.BitLen()+2 {
					t.Fatalf("N=%d e=%d m=%d: too many queries: %d", mod.n, eInt, mInt, out.QueriesUsed)
				}
			}
		}
	}
}

func parseLoggedRat(s string) (*big.Rat, error) {
	i := strings.LastIndex(s, "/2^")
	if i < 0 {
		return nil, fmt.Errorf("not an exact dyadic rational: %q", s)
	}
	num, ok := new(big.Int).SetString(s[:i], 10)
	if !ok || num.Sign() < 0 {
		return nil, fmt.Errorf("bad numerator: %q", s)
	}
	exp, err := strconv.Atoi(s[i+3:])
	if err != nil || exp < 0 {
		return nil, fmt.Errorf("bad exponent: %q", s)
	}
	den := new(big.Int).Lsh(big.NewInt(1), uint(exp))
	return new(big.Rat).SetFrac(num, den), nil
}

func TestRandomHostGeneratedRSA(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	n := key.N
	e := big.NewInt(int64(key.E))
	oracle := rawOracle(n, key.D)

	randomM, err := rand.Int(rand.Reader, n)
	if err != nil {
		t.Fatal(err)
	}
	cases := []*big.Int{
		big.NewInt(0),
		big.NewInt(1),
		new(big.Int).Sub(new(big.Int).Set(n), big.NewInt(1)),
		randomM,
	}

	for _, m := range cases {
		c := new(big.Int).Exp(m, e, n)
		s, err := NewSession(Config{
			N:          n,
			E:          e,
			C:          c,
			MaxQueries: n.BitLen() + 16,
		})
		if err != nil {
			t.Fatal(err)
		}
		out, err := s.Run(oracle)
		if err != nil {
			t.Fatalf("m=%s: %v", m, err)
		}
		if !out.Succeeded() || out.Recovered.Cmp(m) != 0 {
			t.Fatalf("m=%s: status=%s recovered=%v", m, out.Status, out.Recovered)
		}
		if out.QueriesUsed > n.BitLen()+2 {
			t.Fatalf("m=%s: too many queries: %d", m, out.QueriesUsed)
		}
	}
}

func TestTemporaryUnavailableRetainsQueryAndCheckpointResume(t *testing.T) {
	n := big.NewInt(95)
	e := big.NewInt(5)
	d := modInverseOrNil(5, 72)
	m := big.NewInt(73)
	c := new(big.Int).Exp(m, e, n)
	real := rawOracle(n, d)

	calls := 0
	unavailableFirst := ParityOracleFunc(func(x *big.Int) (OracleResult, error) {
		calls++
		if calls <= 5 {
			return ResultUnavailable, nil
		}
		return real.Query(x)
	})

	s, err := NewSession(Config{N: n, E: e, C: c, MaxQueries: 5})
	if err != nil {
		t.Fatal(err)
	}
	out, err := s.Run(unavailableFirst)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != StatusNoUsableOracle || out.Checkpoint == nil {
		t.Fatalf("status=%s checkpoint=%v", out.Status, out.Checkpoint != nil)
	}
	if out.QueriesUsed != 5 || len(out.History) != 5 {
		t.Fatalf("used=%d history=%d", out.QueriesUsed, len(out.History))
	}
	first := out.History[0].Query
	for i, rec := range out.History {
		if rec.Step != 0 || rec.Query != first || rec.Result != "unavailable" {
			t.Fatalf("attempt %d: %+v", i, rec)
		}
	}
	if out.Checkpoint.Step != 0 || out.Checkpoint.Lo != "0" || out.Checkpoint.Hi != "5f" {
		t.Fatalf("unavailable changed interval: %+v", out.Checkpoint)
	}
	if out.Checkpoint.PendingQuery != first {
		t.Fatalf("pending query changed: got %s want %s", out.Checkpoint.PendingQuery, first)
	}

	encoded, err := EncodeCheckpoint(out.Checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	cp, err := DecodeCheckpoint(encoded)
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := ResumeSession(Config{N: n, E: e, C: c, MaxQueries: 32}, cp)
	if err != nil {
		t.Fatal(err)
	}
	out2, err := resumed.Run(unavailableFirst)
	if err != nil {
		t.Fatal(err)
	}
	if !out2.Succeeded() || out2.Recovered.Cmp(m) != 0 {
		t.Fatalf("resume status=%s recovered=%v", out2.Status, out2.Recovered)
	}
	if len(out2.History) < 7 {
		t.Fatalf("history too short: %d", len(out2.History))
	}
	for i := 0; i < 5; i++ {
		if out2.History[i].Result != "unavailable" || out2.History[i].Query != first {
			t.Fatalf("resumed attempt %d changed: %+v", i, out2.History[i])
		}
	}
	if out2.History[5].Step != 0 || out2.History[5].Query != first {
		t.Fatalf("first usable answer advanced before retry: %+v", out2.History[5])
	}
}

func TestIntermittentUnavailableNeverAdvancesQuery(t *testing.T) {
	n := big.NewInt(95)
	e := big.NewInt(7)
	d := modInverseOrNil(7, 72)
	m := big.NewInt(61)
	c := new(big.Int).Exp(m, e, n)
	real := rawOracle(n, d)

	calls := 0
	oracle := ParityOracleFunc(func(x *big.Int) (OracleResult, error) {
		calls++
		if calls%4 == 0 {
			return ResultUnavailable, nil
		}
		return real.Query(x)
	})
	var events []LogEvent
	s, err := NewSession(Config{
		N: n, E: e, C: c, MaxQueries: 32,
		Logger: func(ev LogEvent) { events = append(events, ev) },
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := s.Run(oracle)
	if err != nil {
		t.Fatal(err)
	}
	if !out.Succeeded() || out.Recovered.Cmp(m) != 0 {
		t.Fatalf("status=%s recovered=%v", out.Status, out.Recovered)
	}

	pow2e := new(big.Int).Exp(big.NewInt(2), e, n)
	wantQuery := new(big.Int).Mul(c, pow2e)
	wantQuery.Mod(wantQuery, n)
	wantStep := 0
	for i, rec := range out.History {
		if rec.Query != wantQuery.Text(16) {
			t.Fatalf("attempt %d query=%s want %s", i, rec.Query, wantQuery.Text(16))
		}
		if rec.Step != wantStep {
			t.Fatalf("attempt %d step=%d want %d", i, rec.Step, wantStep)
		}
		switch rec.Result {
		case "even", "odd":
			wantStep++
			wantQuery.Mul(wantQuery, pow2e)
			wantQuery.Mod(wantQuery, n)
		case "unavailable":
			// Same query is retried next attempt.
		default:
			t.Fatalf("unexpected result %q", rec.Result)
		}
	}

	prevStep := -1
	prevLower, prevUpper := "", ""
	for i, ev := range events {
		if ev.Result == "unavailable" {
			if prevStep >= 0 && (ev.Step != prevStep || ev.Lower != prevLower || ev.Upper != prevUpper) {
				t.Fatalf("event %d advanced interval while unavailable: %+v", i, ev)
			}
		} else if prevStep >= 0 && ev.Step != prevStep+1 {
			t.Fatalf("event %d did not advance exactly one step: %+v", i, ev)
		}
		prevStep, prevLower, prevUpper = ev.Step, ev.Lower, ev.Upper
	}
}

func TestAuthenticatedRejectionIsNotEven(t *testing.T) {
	n := big.NewInt(95)
	e := big.NewInt(5)
	m := big.NewInt(73)
	c := new(big.Int).Exp(m, e, n)

	auth := false
	decryptCalls := 0
	fixed, err := NewAuthenticatedOracle(n, func() bool { return auth }, func(x *big.Int) (OracleResult, error) {
		decryptCalls++
		return ResultEven, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if ResultRejected == ResultEven {
		t.Fatal("rejection collides with even")
	}

	for _, bad := range []*big.Int{big.NewInt(-1), new(big.Int).Set(n), new(big.Int).Add(n, big.NewInt(1))} {
		if _, err := fixed.Query(bad); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("invalid query %s: %v", bad, err)
		}
	}
	if decryptCalls != 0 || fixed.Decryptions() != 0 {
		t.Fatal("decrypt reached on invalid or unauthenticated request")
	}

	s, err := NewSession(Config{N: n, E: e, C: c, MaxQueries: 12})
	if err != nil {
		t.Fatal(err)
	}
	out, err := s.Run(fixed)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != StatusNoUsableOracle || out.Succeeded() || out.Recovered != nil {
		t.Fatalf("fixed callback produced usable recovery: %+v", out)
	}
	if decryptCalls != 0 || fixed.Decryptions() != 0 {
		t.Fatalf("decrypt reached before authentication: closure=%d counter=%d", decryptCalls, fixed.Decryptions())
	}
	if out.UsableResponses != 0 || out.Checkpoint == nil {
		t.Fatalf("usable=%d checkpoint=%v", out.UsableResponses, out.Checkpoint != nil)
	}
	for _, rec := range out.History {
		if rec.Result != "rejected" {
			t.Fatalf("uniform rejection became %q", rec.Result)
		}
	}

	auth = true
	got, err := fixed.Query(big.NewInt(1))
	if err != nil {
		t.Fatal(err)
	}
	if got != ResultEven || decryptCalls != 1 || fixed.Decryptions() != 1 {
		t.Fatalf("authenticated call did not reach decrypt exactly once: %v %d %d", got, decryptCalls, fixed.Decryptions())
	}
}

func TestCheckpointTamperAndPartialResume(t *testing.T) {
	n := big.NewInt(95)
	e := big.NewInt(5)
	d := modInverseOrNil(5, 72)
	m := big.NewInt(88)
	c := new(big.Int).Exp(m, e, n)
	oracle := rawOracle(n, d)

	s, err := NewSession(Config{N: n, E: e, C: c, MaxQueries: 2})
	if err != nil {
		t.Fatal(err)
	}
	out, err := s.Run(oracle)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != StatusBudgetExhausted || out.Checkpoint == nil {
		t.Fatalf("status=%s checkpoint=%v", out.Status, out.Checkpoint != nil)
	}
	raw, err := EncodeCheckpoint(out.Checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	base, err := DecodeCheckpoint(raw)
	if err != nil {
		t.Fatal(err)
	}

	clone := func() *Checkpoint {
		cp, err := DecodeCheckpoint(raw)
		if err != nil {
			t.Fatal(err)
		}
		return cp
	}

	wrongN := big.NewInt(101)
	if _, err := ResumeSession(Config{N: wrongN, E: e, C: c, MaxQueries: 20}, base); !errors.Is(err, ErrCheckpointMismatch) {
		t.Fatalf("wrong N: %v", err)
	}

	cp := clone()
	cp.Version = 2
	if _, err := ResumeSession(Config{N: n, E: e, C: c, MaxQueries: 20}, cp); !errors.Is(err, ErrCheckpointInvalid) {
		t.Fatalf("version: %v", err)
	}

	cp = clone()
	if cp.History[1].Result == "even" {
		cp.History[1].Result = "odd"
	} else {
		cp.History[1].Result = "even"
	}
	if _, err := ResumeSession(Config{N: n, E: e, C: c, MaxQueries: 20}, cp); !errors.Is(err, ErrCheckpointInvalid) {
		t.Fatalf("history result tamper: %v", err)
	}

	cp = clone()
	cp.HistoryDigest = strings.Repeat("00", 32)
	if _, err := ResumeSession(Config{N: n, E: e, C: c, MaxQueries: 20}, cp); !errors.Is(err, ErrCheckpointInvalid) {
		t.Fatalf("digest tamper: %v", err)
	}

	cp = clone()
	lo := mustIntHex(cp.Lo)
	cp.Lo = new(big.Int).Add(lo, n).Text(16)
	cp.Hi = new(big.Int).Add(mustIntHex(cp.Hi), n).Text(16)
	if _, err := ResumeSession(Config{N: n, E: e, C: c, MaxQueries: 20}, cp); !errors.Is(err, ErrCheckpointInvalid) {
		t.Fatalf("interval tamper: %v", err)
	}

	cp = clone()
	cp.PendingQuery = "0"
	if _, err := ResumeSession(Config{N: n, E: e, C: c, MaxQueries: 20}, cp); !errors.Is(err, ErrCheckpointInvalid) {
		t.Fatalf("pending tamper: %v", err)
	}

	cp = clone()
	cp.N = "5b"
	if _, err := ResumeSession(Config{N: big.NewInt(91), E: e, C: c, MaxQueries: 20}, cp); !errors.Is(err, ErrCheckpointInvalid) {
		t.Fatalf("N/parameter tamper: %v", err)
	}

	if _, err := ResumeSession(Config{N: n, E: e, C: c, MaxQueries: 1}, base); !errors.Is(err, ErrCheckpointInvalid) {
		t.Fatalf("shrunk budget: %v", err)
	}

	bad := append(append([]byte{}, raw[:len(raw)-1]...), []byte(`,"bogus":true}`)...)
	if _, err := DecodeCheckpoint(bad); !errors.Is(err, ErrCheckpointInvalid) {
		t.Fatalf("unknown field: %v", err)
	}
	if _, err := DecodeCheckpoint(append(append([]byte{}, raw...), []byte(`{}`)...)); !errors.Is(err, ErrCheckpointInvalid) {
		t.Fatalf("trailing data: %v", err)
	}

	resumed, err := ResumeSession(Config{N: n, E: e, C: c, MaxQueries: 20}, base)
	if err != nil {
		t.Fatal(err)
	}
	final, err := resumed.Run(oracle)
	if err != nil {
		t.Fatal(err)
	}
	if !final.Succeeded() || final.Recovered.Cmp(m) != 0 {
		t.Fatalf("resume failed: status=%s recovered=%v", final.Status, final.Recovered)
	}
}

func mustIntHex(s string) *big.Int {
	x, ok := new(big.Int).SetString(s, 16)
	if !ok {
		panic(s)
	}
	return x
}

func TestInvalidConfigAndOracleResults(t *testing.T) {
	n := big.NewInt(95)
	e := big.NewInt(5)
	c := big.NewInt(1)
	cases := []Config{
		{N: nil, E: e, C: c, MaxQueries: 1},
		{N: big.NewInt(1), E: e, C: c, MaxQueries: 1},
		{N: big.NewInt(14), E: e, C: c, MaxQueries: 1},
		{N: n, E: nil, C: c, MaxQueries: 1},
		{N: n, E: big.NewInt(2), C: c, MaxQueries: 1},
		{N: n, E: e, C: big.NewInt(-1), MaxQueries: 1},
		{N: n, E: e, C: new(big.Int).Set(n), MaxQueries: 1},
		{N: n, E: e, C: c, MaxQueries: 0},
	}
	for i, cfg := range cases {
		if _, err := NewSession(cfg); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("case %d: %v", i, err)
		}
	}

	check := []struct {
		n, x *big.Int
	}{
		{nil, big.NewInt(0)},
		{big.NewInt(14), big.NewInt(0)},
		{n, big.NewInt(-1)},
		{n, new(big.Int).Set(n)},
		{n, new(big.Int).Add(n, big.NewInt(1))},
		{n, nil},
	}
	for i, tc := range check {
		if err := CheckQuery(tc.n, tc.x); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("check %d: %v", i, err)
		}
	}

	s, err := NewSession(Config{N: n, E: e, C: big.NewInt(3), MaxQueries: 4})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Run(ParityOracleFunc(func(*big.Int) (OracleResult, error) {
		return OracleResult(200), nil
	})); !errors.Is(err, ErrInvalidOracleResult) {
		t.Fatalf("invalid result: %v", err)
	}

	s, err = NewSession(Config{N: n, E: e, C: big.NewInt(3), MaxQueries: 4})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Run(ParityOracleFunc(func(*big.Int) (OracleResult, error) {
		return ResultEven, errors.New("boom")
	})); !errors.Is(err, ErrCallback) {
		t.Fatalf("callback error: %v", err)
	}
	if _, err := s.Run(nil); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("nil oracle: %v", err)
	}
}

func TestVerificationIsMandatoryForUniqueCandidate(t *testing.T) {
	n := big.NewInt(95)
	e := big.NewInt(5)
	c := big.NewInt(3)
	s, err := NewSession(Config{N: n, E: e, C: c, MaxQueries: 16})
	if err != nil {
		t.Fatal(err)
	}
	out, err := s.Run(ParityOracleFunc(func(*big.Int) (OracleResult, error) {
		return ResultEven, nil
	}))
	if !errors.Is(err, ErrVerificationFailed) {
		t.Fatalf("all-even oracle: out=%v err=%v", out, err)
	}
	if out != nil {
		t.Fatalf("unverified result was returned: %+v", out)
	}
}

func TestLogsContainOnlyObservableExactData(t *testing.T) {
	n := big.NewInt(95)
	e := big.NewInt(7)
	d := modInverseOrNil(7, 72)
	m := big.NewInt(61)
	c := new(big.Int).Exp(m, e, n)

	var events []LogEvent
	s, err := NewSession(Config{
		N:          n,
		E:          e,
		C:          c,
		MaxQueries: 20,
		Logger:     func(ev LogEvent) { events = append(events, ev) },
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := s.Run(rawOracle(n, d))
	if err != nil {
		t.Fatal(err)
	}
	if !out.Succeeded() {
		t.Fatal(out.Status)
	}
	if len(events) != out.QueriesUsed {
		t.Fatalf("events=%d queries=%d", len(events), out.QueriesUsed)
	}
	for i, ev := range events {
		if ev.Kind != "oracle_query" || ev.Attempt != i+1 {
			t.Fatalf("event %d: %+v", i, ev)
		}
		if ev.Result != "even" && ev.Result != "odd" {
			t.Fatalf("event %d result: %+v", i, ev)
		}
		if ev.Query == "" || ev.Lower == "" || ev.Upper == "" {
			t.Fatalf("event %d missing observable data: %+v", i, ev)
		}
		if strings.ContainsAny(ev.Lower, ".eE") || strings.ContainsAny(ev.Upper, ".eE") {
			t.Fatalf("event %d used non-exact formatting: %+v", i, ev)
		}
		if _, err := parseLoggedRat(ev.Lower); err != nil {
			t.Fatalf("event %d: %v", i, err)
		}
		if _, err := parseLoggedRat(ev.Upper); err != nil {
			t.Fatalf("event %d: %v", i, err)
		}
		if _, ok := new(big.Int).SetString(ev.Query, 16); !ok {
			t.Fatalf("event %d query is not observable hex: %q", i, ev.Query)
		}
	}
}

func TestEmptyCheckpointRoundTrip(t *testing.T) {
	n := big.NewInt(95)
	e := big.NewInt(5)
	d := modInverseOrNil(5, 72)
	m := big.NewInt(42)
	c := new(big.Int).Exp(m, e, n)
	s, err := NewSession(Config{N: n, E: e, C: c, MaxQueries: 20})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := EncodeCheckpoint(s.Checkpoint())
	if err != nil {
		t.Fatal(err)
	}
	cp, err := DecodeCheckpoint(raw)
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := ResumeSession(Config{N: n, E: e, C: c, MaxQueries: 20}, cp)
	if err != nil {
		t.Fatal(err)
	}
	out, err := resumed.Run(rawOracle(n, d))
	if err != nil {
		t.Fatal(err)
	}
	if !out.Succeeded() || out.Recovered.Cmp(m) != 0 {
		t.Fatalf("status=%s recovered=%v", out.Status, out.Recovered)
	}
}

func TestConvenienceRecoveryWrappers(t *testing.T) {
	n := big.NewInt(95)
	e := big.NewInt(5)
	d := modInverseOrNil(5, 72)
	m := big.NewInt(42)
	c := new(big.Int).Exp(m, e, n)
	oracle := rawOracle(n, d)

	cfg := Config{N: n, E: e, C: c, MaxQueries: 2}
	out, err := Recover(cfg, oracle)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != StatusBudgetExhausted || out.Checkpoint == nil {
		t.Fatalf("status=%s checkpoint=%v", out.Status, out.Checkpoint != nil)
	}
	cfg.MaxQueries = 20
	final, err := RecoverFromCheckpoint(cfg, out.Checkpoint, oracle)
	if err != nil {
		t.Fatal(err)
	}
	if !final.Succeeded() || final.Recovered.Cmp(m) != 0 {
		t.Fatalf("status=%s recovered=%v", final.Status, final.Recovered)
	}
}
