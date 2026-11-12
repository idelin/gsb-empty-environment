package netsec_test

import (
	"errors"
	"strings"
	"testing"

	netsec "example.org/netsec/q0348"
)

func mustCaps(t *testing.T, s string) netsec.Caps {
	t.Helper()
	caps, err := netsec.ParseCaps(s)
	if err != nil {
		t.Fatalf("ParseCaps(%q): %v", s, err)
	}
	return caps
}

func TestParseUnionAndMatching(t *testing.T) {
	caps := mustCaps(t, `
		allow r pool=app namespace=prod,
		allow w pool=app namespace=prod,
		allow x pool=app namespace=qa,
		allow r pool=spare
	`)

	tests := []struct {
		name      string
		pool      string
		namespace string
		perm      netsec.Permission
		want      bool
	}{
		{"union combines r and w", "app", "prod", netsec.PermRead | netsec.PermWrite, true},
		{"x is separate scope", "app", "prod", netsec.PermExecute, false},
		{"x in qa", "app", "qa", netsec.PermExecute, true},
		{"rw does not leak to qa", "app", "qa", netsec.PermRead, false},
		{"spare read", "spare", "anything", netsec.PermRead, true},
		{"spare write absent", "spare", "anything", netsec.PermWrite, false},
		{"pool exact bytes", "App", "prod", netsec.PermRead, false},
		{"namespace exact bytes", "app", "Prod", netsec.PermRead, false},
		{"unknown scope", "other", "prod", netsec.PermRead, false},
		{"zero permission", "app", "prod", netsec.PermNone, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := caps.Allows(tt.pool, tt.namespace, tt.perm); got != tt.want {
				t.Fatalf("Allows(%q, %q, %v) = %v, want %v",
					tt.pool, tt.namespace, tt.perm, got, tt.want)
			}
		})
	}

	if got := caps.PermissionsFor("app", "prod"); got != netsec.PermRead|netsec.PermWrite {
		t.Fatalf("PermissionsFor(app, prod) = %v, want rw", got)
	}
}

func TestWildcardAndOmittedScope(t *testing.T) {
	caps := mustCaps(t, "allow * pool=app")
	for _, ns := range []string{"prod", "qa", ""} {
		if !caps.Allows("app", ns, netsec.PermAll) {
			t.Fatalf("allow * pool=app should allow all rights in namespace %q", ns)
		}
		if !caps.Allows("app", ns, netsec.PermRead|netsec.PermExecute) {
			t.Fatalf("allow * should allow an r|x request in namespace %q", ns)
		}
	}
	if caps.Allows("other", "prod", netsec.PermRead) {
		t.Fatal("pool-scoped wildcard must not cover another pool")
	}

	global := mustCaps(t, "allow r")
	if !global.Allows("any-pool", "any-namespace", netsec.PermRead) {
		t.Fatal("omitted pool and namespace should cover every scope")
	}

	namespaceOnly := mustCaps(t, "allow w namespace=team-a")
	if !namespaceOnly.Allows("app", "team-a", netsec.PermWrite) {
		t.Fatal("namespace-only rule should cover every pool in that namespace")
	}
	if namespaceOnly.Allows("app", "team-b", netsec.PermWrite) {
		t.Fatal("namespace must be matched exactly")
	}
}

func TestValidSpellings(t *testing.T) {
	valid := []string{
		"allow r",
		"allow rw",
		"allow xwr",
		"allow *",
		"allow rw pool=app",
		"allow r namespace=team-a",
		"allow r pool=app namespace=team-a",
		"  allow\trw\tpool=app namespace=team-a  ",
		"allow r pool=pool-1 namespace=team/a_b.c",
		"allow r pool=数据 namespace=命名空间",
		"allow r pool=a namespace=n, allow w pool=a namespace=n",
	}
	for _, input := range valid {
		t.Run(input, func(t *testing.T) {
			if _, err := netsec.ParseCaps(input); err != nil {
				t.Fatalf("ParseCaps(%q) unexpectedly failed: %v", input, err)
			}
		})
	}
}

func TestInvalidStringsAreAllOrNothing(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantReason string
	}{
		{"empty", "", "empty caps"},
		{"whitespace only", "   \n\t", "empty caps"},
		{"uppercase keyword", "Allow r", "start with allow"},
		{"uppercase keyword 2", "ALLOW r", "start with allow"},
		{"leading keyword typo", "allowx r", "start with allow"},
		{"uppercase permission", "allow R", "invalid permission"},
		{"duplicate permission", "allow rr", "duplicate permission"},
		{"mixed wildcard", "allow rw*", "invalid permission"},
		{"wildcard prefix", "allow *r", "invalid permission"},
		{"missing permission", "allow", "missing permission"},
		{"unknown keyword", "allow r mode=fast", "unknown keyword"},
		{"unknown keyword after scope", "allow r pool=p namespace=n extra=x", "unknown keyword"},
		{"uppercase pool key", "allow r Pool=p", "unknown keyword"},
		{"empty pool", "allow r pool=", "empty name"},
		{"empty namespace", "allow r namespace=", "empty name"},
		{"duplicate pool", "allow r pool=p pool=q", "duplicate pool"},
		{"duplicate namespace", "allow r namespace=n namespace=m", "duplicate namespace"},
		{"pool after namespace", "allow r namespace=n pool=p", "pool must precede namespace"},
		{"wildcard pool", "allow r pool=*", "wildcards are not supported"},
		{"wildcard namespace", "allow r namespace=team/*", "wildcards are not supported"},
		{"extra equals", "allow r pool=p=q", "contains '='"},
		{"trailing comma", "allow r,", "empty rule"},
		{"leading comma", ",allow r", "empty rule"},
		{"doubled comma", "allow r,,allow w", "empty rule"},
		{"second rule missing allow", "allow r, w", "start with allow"},
		{"keyword used as permission", "allow allow", "invalid permission"},
		{"bare pool", "allow r pool", "unknown keyword"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			caps, err := netsec.ParseCaps(tt.input)
			if err == nil {
				t.Fatalf("ParseCaps(%q) = %#v, want error", tt.input, caps)
			}
			if caps != nil {
				t.Fatalf("ParseCaps(%q) returned partial caps %#v", tt.input, caps)
			}
			if !errors.Is(err, netsec.ErrInvalidCaps) {
				t.Fatalf("error %v does not match ErrInvalidCaps", err)
			}
			var parseErr *netsec.ParseError
			if !errors.As(err, &parseErr) {
				t.Fatalf("error %T is not *ParseError", err)
			}
			if tt.wantReason != "" && !strings.Contains(parseErr.Reason, tt.wantReason) {
				t.Fatalf("reason %q does not contain %q", parseErr.Reason, tt.wantReason)
			}
		})
	}
}

func TestParseRejectsBadRuleAtomically(t *testing.T) {
	caps, err := netsec.ParseCaps("allow r pool=app, allow w pool=app bogus=1")
	if err == nil || caps != nil {
		t.Fatalf("mixed valid/invalid input returned caps=%#v err=%v; want nil caps and error", caps, err)
	}
	var parseErr *netsec.ParseError
	if !errors.As(err, &parseErr) || parseErr.Rule != 2 {
		t.Fatalf("parse error %#v should identify rule 2", err)
	}
}

func TestSubsetPolicy(t *testing.T) {
	tests := []struct {
		name string
		old  string
		new  string
		want bool
	}{
		{"identical", "allow rw pool=app namespace=prod", "allow rw pool=app namespace=prod", true},
		{"reduce permission", "allow rw pool=app namespace=prod", "allow r pool=app namespace=prod", true},
		{"add permission", "allow rw pool=app namespace=prod", "allow rwx pool=app namespace=prod", false},
		{"drop scope", "allow rw pool=app namespace=prod", "allow rw", false},
		{"drop pool", "allow rw pool=app namespace=prod", "allow rw namespace=prod", false},
		{"drop namespace", "allow rw pool=app namespace=prod", "allow rw pool=app", false},
		{"other pool", "allow rw pool=app namespace=prod", "allow rw pool=other namespace=prod", false},
		{"other namespace", "allow rw pool=app namespace=prod", "allow rw pool=app namespace=other", false},
		{"old pool scope is broader", "allow rw pool=app", "allow rw pool=app namespace=prod", true},
		{"old namespace scope is broader", "allow rw namespace=prod", "allow rw pool=app namespace=prod", true},
		{"old unrestricted is broader", "allow rw", "allow rw pool=app namespace=prod", true},
		{"old pool does not cover unrestricted namespace", "allow rw pool=app namespace=prod", "allow rw pool=app namespace=prod", true},
		{"empty new", "allow rw", "", true},
		{"empty old", "", "allow r", false},
		{"permission mismatch", "allow x pool=app", "allow r pool=app", false},
		{"all new covered separately", "allow r pool=app, allow w pool=app", "allow r pool=app, allow w pool=app", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var old, new netsec.Caps
			if tt.old != "" {
				old = mustCaps(t, tt.old)
			}
			if tt.new != "" {
				new = mustCaps(t, tt.new)
			}
			if got := new.IsSubsetOf(old); got != tt.want {
				t.Fatalf("%q.IsSubsetOf(%q) = %v, want %v", tt.new, tt.old, got, tt.want)
			}
			if got := old.Covers(new); got != tt.want {
				t.Fatalf("%q.Covers(%q) = %v, want %v", tt.old, tt.new, got, tt.want)
			}
		})
	}
}

func TestSubsetRejectsMalformedManualCaps(t *testing.T) {
	valid := mustCaps(t, "allow r")
	if invalid := (netsec.Caps{{Permissions: 0}}); invalid.Allows("app", "prod", netsec.PermRead) {
		t.Fatal("malformed capability set must not grant anything")
	}
	if (netsec.Caps{{Permissions: 0}}).IsSubsetOf(valid) {
		t.Fatal("zero permission capability must not be a subset")
	}
	if valid.IsSubsetOf(netsec.Caps{{Permissions: netsec.PermRead, Pool: "orphan"}}) {
		t.Fatal("inconsistent manual capability must not cover anything")
	}
	if err := (netsec.Caps{{Permissions: 0}}).Validate(); !errors.Is(err, netsec.ErrInvalidCaps) {
		t.Fatalf("Validate error = %v, want ErrInvalidCaps", err)
	}
}

func TestCanonicalString(t *testing.T) {
	caps := mustCaps(t, "allow rwx pool=app namespace=prod, allow x")
	if got, want := caps.String(), "allow * pool=app namespace=prod, allow x"; got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
	roundTrip := mustCaps(t, caps.String())
	if !roundTrip.IsSubsetOf(caps) || !caps.IsSubsetOf(roundTrip) {
		t.Fatalf("canonical string did not round-trip: %q", caps.String())
	}
}
