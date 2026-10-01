package main

import (
	"strings"
	"testing"
)

func TestParseBasic(t *testing.T) {
	s, err := ParseScript(strings.NewReader(`
# comment
CONN c1 10.0.0.1:1111 10.0.0.2:80
SET delay=2 timewait=7
0 SYN c1 c->s isn=100
1 DATA c1 c->s seq=101 "hi there" delay=3
2 FIN c1 c->s seq=103
3 RST c1 s->c
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if s.Delay != 2 || s.TimeWait != 7 {
		t.Fatalf("defaults = %d/%d", s.Delay, s.TimeWait)
	}
	if len(s.Commands) != 4 {
		t.Fatalf("commands = %d", len(s.Commands))
	}
	d := s.Commands[1]
	if d.Kind != "DATA" || d.Seq != 101 || string(d.Payload) != "hi there" || d.Delay != 3 {
		t.Fatalf("data cmd = %+v", d)
	}
	tuple := s.Tuples["c1"]
	if tuple.ClientPort != 1111 || tuple.ServerIP != "10.0.0.2" {
		t.Fatalf("tuple = %+v", tuple)
	}
}

func TestParseErrors(t *testing.T) {
	cases := []string{
		`0 SYN c9 c->s isn=1`,                                 // unknown conn
		"CONN c1 10.0.0.1 10.0.0.2:80",                        // bad addr
		"CONN c1 10.0.0.1:1 10.0.0.2:2\n0 SYN c1 c->s",        // missing isn
		"CONN c1 10.0.0.1:1 10.0.0.2:2\n0 DATA c1 c->s seq=1", // missing payload
		"CONN c1 10.0.0.1:1 10.0.0.2:2\n0 DATA c1 c->s seq=1 \"unterminated",
		"CONN c1 10.0.0.1:1 10.0.0.2:2\nx SYN c1 c->s isn=1", // bad tick
	}
	for _, src := range cases {
		if _, err := ParseScript(strings.NewReader(src)); err == nil {
			t.Errorf("expected error for %q", src)
		}
	}
}
