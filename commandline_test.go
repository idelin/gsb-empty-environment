package netsec

import (
	"errors"
	"testing"
)

func TestIsBatchTarget(t *testing.T) {
	tests := []struct {
		name    string
		target  string
		pathext string
		want    bool
	}{
		{name: "bat lower case", target: `C:\tools\run.bat`, want: true},
		{name: "cmd upper case", target: `C:\tools\RUN.CMD`, want: true},
		{name: "bat with trailing spaces", target: `C:\tools\run.bat   `, want: true},
		{name: "bat with trailing dots", target: `C:\tools\run.bat...`, want: true},
		{name: "cmd with spaces and dots", target: `C:\tools\run.CMD . . `, want: true},
		{name: "exe is not batch", target: `C:\tools\app.exe`, want: false},
		{name: "explicit other extension", target: `C:\tools\run.com`, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := Env{PathExt: tt.pathext}
			if got := env.IsBatchTarget(tt.target); got != tt.want {
				t.Fatalf("IsBatchTarget(%q) = %v, want %v", tt.target, got, tt.want)
			}
		})
	}
}

func TestIsBatchTargetWithPathExtCompletion(t *testing.T) {
	base := t.TempDir()
	target := joinPath(base, "deploy")
	batchTarget := joinPath(base, "deploy.bat")

	tests := []struct {
		name    string
		path    string
		pathext string
		files   []string
		target  string
		want    bool
	}{
		{
			name:    "bat completed from PATHEXT",
			path:    base,
			pathext: ".COM;.EXE;.BAT;.CMD",
			files:   []string{batchTarget},
			target:  target,
			want:    true,
		},
		{
			name:    "cmd completed from PATHEXT",
			path:    base,
			pathext: ".CMD;.BAT",
			files:   []string{joinPath(base, "deploy.CMD")},
			target:  target,
			want:    true,
		},
		{
			name:    "PATHEXT completion is case insensitive",
			path:    base,
			pathext: ".bat",
			files:   []string{joinPath(base, "DEPLOY.BAT")},
			target:  target,
			want:    true,
		},
		{
			name:    "non-batch PATHEXT match",
			path:    base,
			pathext: ".EXE;.BAT",
			files:   []string{joinPath(base, "deploy.exe")},
			target:  target,
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, file := range tt.files {
				createFile(t, file)
			}

			env := Env{Path: tt.path, PathExt: tt.pathext}
			if got := env.IsBatchTarget(tt.target); got != tt.want {
				t.Fatalf("IsBatchTarget(%q) = %v, want %v", tt.target, got, tt.want)
			}
		})
	}
}

func TestValidateArguments(t *testing.T) {
	dangerous := []string{
		`"&calc.exe`,
		"100%",
		"a&b",
		"a|b",
		"a<b",
		"a>b",
		"a^b",
		"a!b",
		"line\nbreak",
		"line\rbreak",
	}

	for _, arg := range dangerous {
		t.Run(arg, func(t *testing.T) {
			if err := ValidateArguments(`C:\tools\run.bat`, []string{arg}); err == nil {
				t.Fatalf("ValidateArguments accepted %q for a batch target", arg)
			}
			if err := ValidateArguments(`C:\tools\app.exe`, []string{arg}); err != nil {
				t.Fatalf("ValidateArguments rejected %q for an exe target: %v", arg, err)
			}
		})
	}

	safeBatchArgs := []string{"plain", "C:\\tools", "a+b", "a;b", "a(b)", ""}
	if err := ValidateArguments(`C:\tools\run.CMD`, safeBatchArgs); err != nil {
		t.Fatalf("safe batch arguments were rejected: %v", err)
	}
}

func TestSafeCommandRejectsBatchInjection(t *testing.T) {
	_, err := SafeCommand(`C:\tools\run.bat`, `"&calc.exe`)
	if !errors.Is(err, ErrUnsafeArgument) {
		t.Fatalf("SafeCommand error = %v, want ErrUnsafeArgument", err)
	}

	_, err = SafeCommand(`C:\tools\run.bat...`, `"&calc.exe`)
	if !errors.Is(err, ErrUnsafeArgument) {
		t.Fatalf("SafeCommand with trailing-dot batch target error = %v, want ErrUnsafeArgument", err)
	}
}

func TestEscapeArgument(t *testing.T) {
	tests := []struct {
		name string
		arg  string
		want string
	}{
		{name: "plain", arg: "plain", want: `plain`},
		{name: "space", arg: `C:\Program Files\app`, want: `"C:\Program Files\app"`},
		{name: "quote", arg: `a"b`, want: `"a\"b"`},
		{name: "backslashes before quote", arg: `a\\\\"b`, want: `"a\\\\\\\\\"b"`},
		{name: "trailing backslashes without quote", arg: `C:\dir\\`, want: `C:\dir\\`},
		{name: "trailing backslashes with quote", arg: `C:\dir with space\\`, want: `"C:\dir with space\\\\"`},
		{name: "backslashes without quote", arg: `C:\dir\file`, want: `C:\dir\file`},
		{name: "empty", arg: "", want: `""`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := EscapeArgument(tt.arg); got != tt.want {
				t.Fatalf("EscapeArgument(%q) = %q, want %q", tt.arg, got, tt.want)
			}
		})
	}
}

func TestEscapeCommandLineForNormalExe(t *testing.T) {
	got, err := EscapeCommandLine(`C:\Program Files\tool.exe`, []string{`C:\Program Files\input`, `a"b`})
	if err != nil {
		t.Fatalf("EscapeCommandLine returned an error: %v", err)
	}

	want := `"C:\Program Files\tool.exe" "C:\Program Files\input" "a\"b"`
	if got != want {
		t.Fatalf("EscapeCommandLine() = %q, want %q", got, want)
	}
}

func TestEscapeCommandLineRejectsNUL(t *testing.T) {
	if _, err := EscapeCommandLine(`C:\tools\app.exe`, []string{"bad\x00arg"}); err == nil {
		t.Fatal("EscapeCommandLine accepted a NUL byte")
	}
}
