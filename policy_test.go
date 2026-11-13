package netsec_test

import (
	"errors"
	"strings"
	"testing"

	"example.org/netsec/q0799"
)

func TestRejectsRequiredAttackExamples(t *testing.T) {
	t.Parallel()

	policy := netsec.Policy{AllowedDrives: []string{"C"}}
	tests := []string{
		"//evil/share",
		`\\.\UNC\evil\share`,
		`\\?\UNC\evil\share`,
		`\??\UNC\evil\share`,
		`\GLOBALROOT\Device\Mup\evil\share`,
		"file://host/x",
		"C:foo",
		"NUL.txt",
		"a.txt:$DATA",
	}
	for _, path := range tests {
		path := path
		t.Run(path, func(t *testing.T) {
			if err := policy.Check(path); err == nil {
				t.Fatalf("expected %q to be rejected", path)
			} else if !errors.Is(err, netsec.ErrUnsafePath) {
				t.Fatalf("expected ErrUnsafePath, got %v", err)
			}
		})
	}
}

func TestRejectsLeadingSeparatorVariantsBeforeFolding(t *testing.T) {
	t.Parallel()

	for _, path := range []string{
		"///evil/share",
		`\\\\evil\share`,
		`\/evil/share`,
		`/\evil/share`,
		`\\?\C:\Windows`,
		"  //evil/share",
		"file:////evil/share",
	} {
		if err := (netsec.Policy{AllowedDrives: []string{"C"}}).Check(path); err == nil {
			t.Errorf("expected %q to be rejected", path)
		}
	}
}

func TestRejectsWebDAVAndDavWWWRoot(t *testing.T) {
	t.Parallel()

	for _, path := range []string{
		`\\server@SSL\DavWWWRoot\share`,
		`\\server@443\DavWWWRoot\share`,
		`/server@ssl/DavWWWRoot/share`,
		`https://server@SSL/share`,
		`\\server\DavWWWRoot\share`,
	} {
		if err := (netsec.Policy{}).Check(path); err == nil {
			t.Errorf("expected %q to be rejected", path)
		}
	}
}

func TestRejectsDrivePolicyViolations(t *testing.T) {
	t.Parallel()

	policy := netsec.Policy{AllowedDrives: []string{"C:", `c:\`}}
	for _, path := range []string{
		`D:\data\file.txt`,
		"D:/data/file.txt",
		"C:",
		`C:relative\file.txt`,
		`C:\data\file.txt:$DATA`,
		`C:\data\file.txt:stream`,
	} {
		if err := policy.Check(path); err == nil {
			t.Errorf("expected %q to be rejected", path)
		}
	}

	for _, path := range []string{
		`C:\data\file.txt`,
		"C:/data/file.txt",
		"data/file.txt",
		"file:///C:/data/file.txt",
		"file:/C:/data/file.txt",
		"file:C:/data/file.txt",
	} {
		if err := policy.Check(path); err != nil {
			t.Errorf("expected %q to be allowed, got %v", path, err)
		}
	}
}

func TestNoDriveWhitelistRejectsDrivePaths(t *testing.T) {
	t.Parallel()

	if err := (netsec.Policy{}).Check(`C:\data\file.txt`); err == nil {
		t.Fatal("an empty drive allowlist must reject drive-qualified paths")
	}
	if err := (netsec.Policy{}).Check("relative/file.txt"); err != nil {
		t.Fatalf("relative path should be allowed: %v", err)
	}
}

func TestRejectsAmbiguousColonAndPercentEncoding(t *testing.T) {
	t.Parallel()

	for _, path := range []string{
		"a.txt:stream",
		"a.txt:$DATA",
		"a.txt:$data",
		"dir/a:b",
		`dir\file%5cname`,
		`dir\file%5Cname`,
	} {
		if err := (netsec.Policy{}).Check(path); err == nil {
			t.Errorf("expected %q to be rejected", path)
		}
	}
	if err := (netsec.Policy{}).Check("100%25-complete.txt"); err != nil {
		t.Fatalf("percent encoding must not be decoded or broadly rejected: %v", err)
	}
}

func TestRejectsReservedDeviceNames(t *testing.T) {
	t.Parallel()

	for _, path := range []string{
		"NUL",
		"nul.txt",
		"NUL.",
		"NUL ",
		"NUL..txt",
		"CON.txt",
		"aux.tar.gz",
		"COM1.txt",
		"LPT9.log",
		`dir\CON`,
		`C:\data\NUL.txt`,
		"file:///C:/data/NUL.txt",
	} {
		policy := netsec.Policy{AllowedDrives: []string{"C"}}
		if err := policy.Check(path); err == nil {
			t.Errorf("expected %q to be rejected", path)
		}
	}

	for _, path := range []string{"COM10.txt", "LPT10.txt", "CONSOLE.txt", ".NUL"} {
		if err := (netsec.Policy{}).Check(path); err != nil {
			t.Errorf("expected %q to be allowed, got %v", path, err)
		}
	}
}

func TestResolverResultIsChecked(t *testing.T) {
	t.Parallel()

	calls := 0
	policy := netsec.Policy{
		AllowedDrives: []string{"C"},
		ResolvePath: func(path string) (string, error) {
			calls++
			if path == "link.txt" {
				return `\\evil\share`, nil
			}
			return path, nil
		},
	}

	if err := policy.Check("link.txt"); err == nil {
		t.Fatal("expected a resolver result pointing at a UNC path to be rejected")
	} else if !errors.Is(err, netsec.ErrUnsafePath) {
		t.Fatalf("expected ErrUnsafePath, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected resolver to be called once, got %d", calls)
	}
}

func TestResolverIsNotCalledAfterLexicalRejection(t *testing.T) {
	t.Parallel()

	calls := 0
	policy := netsec.Policy{
		AllowedDrives: []string{"C"},
		ResolvePath: func(path string) (string, error) {
			calls++
			return path, nil
		},
	}
	if err := policy.Check("//evil/share"); err == nil {
		t.Fatal("expected leading separators to be rejected")
	}
	if calls != 0 {
		t.Fatalf("resolver must not run before lexical admission, got %d calls", calls)
	}
}

func TestResolverFailureFailsClosed(t *testing.T) {
	t.Parallel()

	errBoom := errors.New("cannot resolve")
	policy := netsec.Policy{
		ResolvePath: func(string) (string, error) {
			return "", errBoom
		},
	}
	if err := policy.Check("file.txt"); err == nil {
		t.Fatal("expected resolver error to fail closed")
	} else if !errors.Is(err, errBoom) {
		t.Fatalf("expected resolver error to be wrapped, got %v", err)
	}
}

func TestResolverCycleIsRejected(t *testing.T) {
	t.Parallel()

	policy := netsec.Policy{
		ResolvePath: func(path string) (string, error) {
			switch path {
			case "a":
				return "b", nil
			case "b":
				return "a", nil
			default:
				return path, nil
			}
		},
	}
	if err := policy.Check("a"); err == nil {
		t.Fatal("expected a resolution cycle to be rejected")
	}
}

func TestFileURLRules(t *testing.T) {
	t.Parallel()

	policy := netsec.Policy{AllowedDrives: []string{"C"}}
	for _, path := range []string{
		"file://localhost/C:/data",
		"file://./C:/data",
		"file://C:/data",
		`file://\evil\share`,
		`file://\\evil\share`,
		`file:///\evil\share`,
		"file:///D:/data",
	} {
		if err := policy.Check(path); err == nil {
			t.Errorf("expected %q to be rejected", path)
		}
	}

	if err := policy.Check("file:///C:/data/file.txt"); err != nil {
		t.Fatalf("hostless local file URL should be allowed: %v", err)
	}
}

func TestReasonIsReported(t *testing.T) {
	t.Parallel()

	err := (netsec.Policy{}).Check("//evil/share")
	var pathErr *netsec.Error
	if !errors.As(err, &pathErr) {
		t.Fatalf("expected *netsec.Error, got %T", err)
	}
	if pathErr.Reason != netsec.ReasonLeadingSeparators {
		t.Fatalf("expected leading separator reason, got %s", pathErr.Reason)
	}
	if !strings.Contains(pathErr.Error(), "leading_separators") {
		t.Fatalf("unexpected error text: %s", pathErr)
	}
}
