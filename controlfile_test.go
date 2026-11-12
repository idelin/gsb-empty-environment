package netsec

import (
	"errors"
	"strings"
	"testing"
)

func TestParseControlFile(t *testing.T) {
	data := `
# trusted extension
comment = 'demo'
name = 'demo'
default_version = '1.0'
trusted = true
superuser = false
requires = 'dep_one, dep_two'
module_pathname = '$libdir/demo'
`
	ext, err := ParseControlFile([]byte(data))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if ext.Name != "demo" || !ext.Trusted || ext.Superuser {
		t.Fatalf("unexpected metadata: %+v", ext)
	}
	if len(ext.Requires) != 2 || ext.Requires[0] != "dep_one" || ext.Requires[1] != "dep_two" {
		t.Fatalf("unexpected requires: %#v", ext.Requires)
	}
	if ext.ModulePathname != "$libdir/demo" {
		t.Fatalf("module_pathname = %q", ext.ModulePathname)
	}
}

func TestParseControlFileSuperuserDefaultsTrue(t *testing.T) {
	ext, err := ParseControlFileReader(strings.NewReader("trusted = true\n"))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if !ext.Superuser {
		t.Fatal("superuser did not default to true")
	}
}

func TestParseControlFileRejectsMalformedValues(t *testing.T) {
	if _, err := ParseControlFile([]byte("trusted = maybe\n")); err == nil {
		t.Fatal("invalid boolean accepted")
	}
	if _, err := ParseControlFile([]byte("trusted = 'unterminated\n")); err == nil {
		t.Fatal("unterminated quote accepted")
	}
}

func TestParseParsedControlMetadata(t *testing.T) {
	ext, err := ControlFileFromMap(map[string]string{
		"name":      "'demo'",
		"trusted":   "true",
		"superuser": "false",
		"requires":  "'dep_one, dep_two'",
	})
	if err != nil {
		t.Fatalf("map conversion failed: %v", err)
	}
	if ext.Name != "demo" || !ext.Trusted || ext.Superuser || len(ext.Requires) != 2 {
		t.Fatalf("unexpected metadata: %+v", ext)
	}
}

func TestCheckControlFileAppliesPolicy(t *testing.T) {
	data := []byte(`
name = 'demo'
trusted = true
superuser = false
requires = 'dep'
`)
	installed := map[string]Extension{
		"dep": {Name: "dep", Trusted: true},
	}
	if err := CheckControlFile(Role{Name: "alice"}, data, installed); err != nil {
		t.Fatalf("valid control file rejected: %v", err)
	}

	err := CheckControlFile(Role{Name: "alice"}, data)
	if !errors.Is(err, ErrDependencyNotInstalled) {
		t.Fatalf("error = %v, want ErrDependencyNotInstalled", err)
	}
}

func TestCheckExtensionInstallAcceptsRawMap(t *testing.T) {
	role := Role{Name: "alice"}
	raw := map[string]string{
		"name":      "'demo'",
		"trusted":   "true",
		"superuser": "false",
	}
	if err := CheckExtensionInstall(role, raw); err != nil {
		t.Fatalf("raw metadata rejected: %v", err)
	}
}
