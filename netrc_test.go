package netsec

import "testing"

func TestParseNetrc(t *testing.T) {
	t.Parallel()

	const data = `machine BÜCHER.Example. login alice password "correct horse battery staple"
machine example.com:8443 login bob password secret
macdef init
machine attacker.example login evil password stolen
unbalanced "quote in macro body

default login fallback password fallback-secret
`

	netrc, err := ParseNetrcString(data)
	if err != nil {
		t.Fatalf("ParseNetrcString: %v", err)
	}

	credential, ok := netrc.Lookup("bücher.example")
	if !ok {
		t.Fatal("IDN machine entry not found")
	}
	if credential.Login != "alice" || credential.Password != "correct horse battery staple" {
		t.Fatalf("unexpected IDN credential: %#v", credential)
	}

	credential, ok = netrc.Lookup("example.com")
	if !ok {
		t.Fatal("portless lookup of machine with port failed")
	}
	if credential.Login != "bob" || credential.Password != "secret" {
		t.Fatalf("unexpected port credential: %#v", credential)
	}

	if _, ok := netrc.Lookup("attacker.example"); ok {
		t.Fatal("machine entry inside macdef body was parsed")
	}

	credential, ok = netrc.Default()
	if !ok {
		t.Fatal("default entry not found")
	}
	if credential.Login != "fallback" || credential.Password != "fallback-secret" {
		t.Fatalf("unexpected default credential: %#v", credential)
	}
}

func TestParseNetrcFieldsAcrossLines(t *testing.T) {
	t.Parallel()

	data := `machine example.com
login alice
password "two words"
`
	netrc, err := ParseNetrcString(data)
	if err != nil {
		t.Fatal(err)
	}
	credential, ok := netrc.Lookup("example.com")
	if !ok {
		t.Fatal("machine entry not found")
	}
	if credential.Login != "alice" || credential.Password != "two words" {
		t.Fatalf("unexpected credential: %#v", credential)
	}
}

func TestParseNetrcHashInPassword(t *testing.T) {
	t.Parallel()

	netrc, err := ParseNetrcString("# full-line comment\nmachine example.com login alice password p#ss\n")
	if err != nil {
		t.Fatal(err)
	}
	credential, ok := netrc.Lookup("example.com")
	if !ok {
		t.Fatal("machine entry not found")
	}
	if credential.Password != "p#ss" {
		t.Fatalf("Password = %q, want p#ss", credential.Password)
	}
}
