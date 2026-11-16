package netsec_test

import (
	"errors"
	"testing"

	"example.org/netsec/q0799"
)

func TestRejectsRequiredExamples(t *testing.T) {
	tests := []struct {
		name string
		path string
		want error
	}{
		{name: "forward-slash UNC", path: "//evil/share", want: netsec.ErrUNCPath},
		{name: "backslash UNC", path: `\\evil\share`, want: netsec.ErrUNCPath},
		{name: "UNC device prefix", path: `\\?\UNC\evil\share`, want: netsec.ErrUNCPath},
		{name: "device namespace", path: `\\.\C:\Windows`, want: netsec.ErrUNCPath},
		{name: "object-manager namespace", path: `\??\C:\Windows`, want: netsec.ErrDevicePath},
		{name: "global root", path: `\GLOBALROOT\Device\HarddiskVolume0`, want: netsec.ErrDevicePath},
		{name: "file URL with host", path: "file://host/x", want: netsec.ErrFileURLWithHost},
		{name: "drive-relative", path: `C:foo`, want: netsec.ErrDriveRelative},
		{name: "reserved name with extension", path: `NUL.txt`, want: netsec.ErrReservedName},
		{name: "alternate data stream", path: `a.txt:$DATA`, want: netsec.ErrAlternateDataStream},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := netsec.CheckPath(test.path, netsec.Config{AllowedDrives: []string{"C"}})
			if !errors.Is(err, test.want) {
				t.Fatalf("CheckPath(%q) error = %v, want %v", test.path, err, test.want)
			}
		})
	}
}

func TestRejectsBypassVariants(t *testing.T) {
	tests := []struct {
		name string
		path string
		want error
	}{
		{name: "mixed leading separators", path: `/\evil/share`, want: netsec.ErrUNCPath},
		{name: "three forward slashes", path: "///evil/share", want: netsec.ErrUNCPath},
		{name: "many backslashes", path: `\\\\evil\share`, want: netsec.ErrUNCPath},
		{name: "embedded device namespace", path: `C:\safe\..\\?\C:\Windows`, want: netsec.ErrDevicePath},
		{name: "lowercase global root", path: `C:\globalroot\Device`, want: netsec.ErrDevicePath},
		{name: "webdav SSL", path: `C:\host@SSL\share`, want: netsec.ErrWebDAVPath},
		{name: "webdav port", path: `C:\host@443\share`, want: netsec.ErrWebDAVPath},
		{name: "davwwwroot", path: `C:\share\DavWWWRoot\file`, want: netsec.ErrWebDAVPath},
		{name: "encoded separator", path: `C:\share\%5csecret`, want: netsec.ErrEncodedSeparator},
		{name: "uppercase encoded separator", path: `C:\share\%5Csecret`, want: netsec.ErrEncodedSeparator},
		{name: "colon after drive", path: `C:\share\file:stream`, want: netsec.ErrColon},
		{name: "colon without drive", path: `share\file:stream`, want: netsec.ErrColon},
		{name: "lowercase ADS", path: `C:\share\a.txt:$data`, want: netsec.ErrAlternateDataStream},
		{name: "reserved child component", path: `C:\share\aux.log`, want: netsec.ErrReservedName},
		{name: "reserved trailing dot", path: `C:\share\COM1.`, want: netsec.ErrReservedName},
		{name: "reserved trailing spaces", path: "C:\\share\\LPT9   ", want: netsec.ErrReservedName},
		{name: "reserved space before extension", path: `C:\share\NUL .txt`, want: netsec.ErrReservedName},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := netsec.CheckPath(test.path, netsec.Config{AllowedDrives: []string{"C"}})
			if !errors.Is(err, test.want) {
				t.Fatalf("CheckPath(%q) error = %v, want %v", test.path, err, test.want)
			}
		})
	}
}

func TestAllowsLocalPaths(t *testing.T) {
	cfg := netsec.Config{AllowedDrives: []string{"C:"}}
	tests := []string{
		`C:\data\file.txt`,
		`c:/data/file.txt`,
		`data\file.txt`,
		`data/file.txt`,
		`COM10`,
		`CONSOLE.txt`,
		`%41.txt`,
		`share\NULL`,
		`C:\share\device\file.txt`,
	}

	for _, path := range tests {
		t.Run(path, func(t *testing.T) {
			if err := netsec.CheckPath(path, cfg); err != nil {
				t.Fatalf("CheckPath(%q) = %v, want nil", path, err)
			}
		})
	}
}

func TestDriveAllowlist(t *testing.T) {
	cfg := netsec.Config{AllowedDrives: []string{"c"}}
	if err := netsec.CheckPath(`C:\data\file.txt`, cfg); err != nil {
		t.Fatalf("allowed drive rejected: %v", err)
	}
	if err := netsec.CheckPath(`D:\data\file.txt`, cfg); !errors.Is(err, netsec.ErrDriveNotAllowed) {
		t.Fatalf("disallowed drive error = %v, want %v", err, netsec.ErrDriveNotAllowed)
	}
	if err := netsec.CheckPath(`D:\data\file.txt`, netsec.Config{}); !errors.Is(err, netsec.ErrDriveNotAllowed) {
		t.Fatalf("empty allowlist error = %v, want %v", err, netsec.ErrDriveNotAllowed)
	}
}

func TestResolver(t *testing.T) {
	cfg := netsec.Config{
		AllowedDrives: []string{"C"},
		Resolve: func(string) (string, error) {
			return `C:\data\real.txt`, nil
		},
	}
	if err := netsec.CheckPath(`C:\data\link`, cfg); err != nil {
		t.Fatalf("local resolved target rejected: %v", err)
	}

	cfg.Resolve = func(string) (string, error) {
		return `\\evil\share`, nil
	}
	err := netsec.CheckPath(`C:\data\link`, cfg)
	if !errors.Is(err, netsec.ErrLinkTarget) || !errors.Is(err, netsec.ErrUNCPath) {
		t.Fatalf("network resolved target error = %v, want link and UNC errors", err)
	}

	cfg.Resolve = func(string) (string, error) {
		return "file://host/x", nil
	}
	if err := netsec.CheckPath(`C:\data\link`, cfg); !errors.Is(err, netsec.ErrLinkTarget) {
		t.Fatalf("URL resolved target error = %v, want %v", err, netsec.ErrLinkTarget)
	}

	cfg.Resolve = func(string) (string, error) {
		return `D:\data\real.txt`, nil
	}
	if err := netsec.CheckPath(`C:\data\link`, cfg); !errors.Is(err, netsec.ErrDriveNotAllowed) {
		t.Fatalf("cross-drive target error = %v, want %v", err, netsec.ErrDriveNotAllowed)
	}
}

func TestResolverErrorsAndOrdering(t *testing.T) {
	called := false
	cfg := netsec.Config{
		Resolve: func(string) (string, error) {
			called = true
			return "", nil
		},
	}
	if err := netsec.CheckPath(`NUL.txt`, cfg); !errors.Is(err, netsec.ErrReservedName) {
		t.Fatalf("lexical error = %v, want %v", err, netsec.ErrReservedName)
	}
	if called {
		t.Fatal("resolver was called before lexical rejection")
	}

	cfg.Resolve = func(string) (string, error) {
		return "", errors.New("inspect failed")
	}
	if err := netsec.CheckPath(`safe\file.txt`, cfg); !errors.Is(err, netsec.ErrResolve) {
		t.Fatalf("resolver failure = %v, want %v", err, netsec.ErrResolve)
	}

	cfg.Resolve = func(string) (string, error) {
		return "", nil
	}
	if err := netsec.CheckPath(`safe\file.txt`, cfg); !errors.Is(err, netsec.ErrLinkTarget) {
		t.Fatalf("empty target failure = %v, want %v", err, netsec.ErrLinkTarget)
	}
}

func TestCheckerIsReusable(t *testing.T) {
	checker := netsec.NewChecker(netsec.Config{AllowedDrives: []string{"C"}})
	for _, path := range []string{`C:\one`, `C:\two`} {
		if err := checker.Check(path); err != nil {
			t.Fatalf("Check(%q) = %v, want nil", path, err)
		}
	}
	if err := checker.Check(`\\evil\share`); !errors.Is(err, netsec.ErrUNCPath) {
		t.Fatalf("Check(UNC) = %v, want %v", err, netsec.ErrUNCPath)
	}
}

func FuzzCheckPathDoesNotPanic(f *testing.F) {
	for _, path := range []string{
		"",
		"//evil/share",
		`\\?\UNC\evil\share`,
		"file://host/x",
		`C:foo`,
		`NUL.txt`,
		`a.txt:$DATA`,
	} {
		f.Add(path)
	}
	f.Fuzz(func(t *testing.T, path string) {
		_ = netsec.CheckPath(path, netsec.Config{AllowedDrives: []string{"C", "D"}})
	})
}
