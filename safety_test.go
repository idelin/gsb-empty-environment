package parity_test

import (
	"bytes"
	"context"
	"errors"
	"math/big"
	"reflect"
	"strings"
	"testing"

	parity "prescreen.local/ps-2b994de5fb"
)

func TestFixedCallbackUniformRejectionIsNeverEven(t *testing.T) {
	k := makeTinyKey(13, 17)
	fc := parity.NewFixedCallback(k.oracle())

	// Pre-auth: identical rejection for every x, including invalid x.
	msgs := map[string]bool{}
	for _, x := range []*big.Int{big.NewInt(0), big.NewInt(1), big.NewInt(2), new(big.Int).Sub(k.N, big.NewInt(1)), big.NewInt(-5), new(big.Int).Add(k.N, big.NewInt(9))} {
		p, err := fc.QueryParity(x)
		if p == parity.ParityEven {
			t.Fatalf("rejection must not be even for x=%s", x)
		}
		if p != parity.ParityRejected {
			t.Fatalf("expected ParityRejected marker, got %v", p)
		}
		if !errors.Is(err, parity.ErrAuthRequired) || !errors.Is(err, parity.ErrNoOracle) {
			t.Fatalf("x=%s: err=%v must wrap ErrAuthRequired/ErrNoOracle", x, err)
		}
		msgs[err.Error()] = true
	}
	if len(msgs) != 1 {
		t.Fatalf("rejection must be uniform, got %d distinct messages: %v", len(msgs), msgs)
	}

	p := paramsFor(k, 42)
	rep, err := parity.RunExperiment(context.Background(), p, fc.QueryParity, parity.Options{QueryBudget: 5})
	if !errors.Is(err, parity.ErrNoOracle) {
		t.Fatalf("experiment must report no oracle, got err=%v", err)
	}
	if !rep.NoOracle || rep.OracleUsable || rep.Recovered {
		t.Fatalf("bad report: %+v", rep)
	}
	if rep.Result.Status != parity.StatusNoOracle || rep.Result.Plaintext != nil {
		t.Fatalf("fixed callback must not recover (must not silently yield m=0): %+v", rep.Result)
	}

	// A host-owned arbitrary uniform denial is also never read as even.
	deny := parity.RejectAll(errors.New("403 forbidden"))
	_, err = parity.Recover(p, deny, parity.Options{QueryBudget: 3})
	if !errors.Is(err, parity.ErrNoOracle) {
		t.Fatalf("generic uniform denial should classify as no-oracle, got %v", err)
	}

	// After authentication the wrapped oracle works.
	fc.Authenticate()
	res, err := parity.Recover(p, fc.QueryParity, parity.Options{})
	if err != nil || res.Plaintext.Int64() != 42 {
		t.Fatalf("post-auth recovery: %v %v", res, err)
	}
}

func TestLogContainsOnlyObservableAndDerivation(t *testing.T) {
	k := makeTinyKey(11, 13)
	var buf bytes.Buffer
	text := parity.NewTextLogger(&buf)
	rec := parity.NewRecordingLogger()
	multi := parity.LoggerFunc(func(ev parity.Event) {
		text.LogEvent(ev)
		rec.LogEvent(ev)
	})

	res, err := parity.Recover(paramsFor(k, 99), k.oracle(), parity.Options{Logger: multi, Blinding: parity.BlindNone})
	if err != nil {
		t.Fatal(err)
	}

	// Event type structurally has no plaintext/secret/decrypted field.
	rt := reflect.TypeOf(parity.Event{})
	for i := 0; i < rt.NumField(); i++ {
		name := strings.ToLower(rt.Field(i).Name)
		for _, banned := range []string{"plaintext", "secret", "decrypted", "privatekey"} {
			if strings.Contains(name, banned) {
				t.Fatalf("Event must not carry field %s", rt.Field(i).Name)
			}
		}
	}

	out := buf.String()
	if !strings.Contains(out, "kind=query") || !strings.Contains(out, "x=") {
		t.Fatalf("log missing observable queries:\n%s", out)
	}
	if !strings.Contains(out, "lower=") || !strings.Contains(out, "upper=") {
		t.Fatalf("log missing derived intervals:\n%s", out)
	}
	if strings.Contains(out, "plaintext=") || strings.Contains(strings.ToLower(out), "decrypted=") {
		t.Fatalf("log contains a forbidden plaintext marker:\n%s", out)
	}

	// Every logged query x matches observable history and is in range.
	N := k.N
	for _, ev := range rec.Events() {
		if ev.X == "" {
			continue
		}
		x, ok := new(big.Int).SetString(ev.X, 16)
		if !ok {
			t.Fatalf("bad logged x %q", ev.X)
		}
		if x.Sign() < 0 || x.Cmp(N) >= 0 {
			t.Fatalf("logged x out of range %s", x)
		}
	}

	// Result history digest is stable.
	if parity.DigestHistory(res.History) != res.HistoryDigest {
		t.Fatal("history digest mismatch")
	}
}
