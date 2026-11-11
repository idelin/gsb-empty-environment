package ctxescape

import (
	"bytes"
	"testing"
)

func TestIndexTagEndMixedCase(t *testing.T) {
	cases := []struct {
		s    string
		want int
	}{
		{`</script>`, 9},
		{`</sCript >`, 10},
		{`</SCRIPT>`, 9},
		{`</script`, -1},
		{`</scriptx>`, -1},
	}
	for _, c := range cases {
		got := indexTagEnd([]byte(c.s), "script")
		if got != c.want {
			t.Errorf("indexTagEnd(%q)=%d want %d", c.s, got, c.want)
		}
	}
}

func TestScriptEndPrefix(t *testing.T) {
	cases := []struct {
		s    string
		want int
	}{
		{`<`, 0},
		{`</`, 0},
		{`</sCri`, 0},
		{`abc</`, 3},
		{`abc`, -1},
		{`</script`, 0},
	}
	for _, c := range cases {
		got := scriptEndPrefix([]byte(c.s))
		if got != c.want {
			t.Errorf("scriptEndPrefix(%q)=%d want %d", c.s, got, c.want)
		}
	}
}

func TestByteWiseMixedCaseScript(t *testing.T) {
	in := `<sCript>"</sCript >`
	var buf bytes.Buffer
	w := NewWriter(&buf)
	for i := 0; i < len(in); i++ {
		if _, err := w.Write([]byte{in[i]}); err != nil {
			t.Fatalf("byte %d: %v", i, err)
		}
		t.Logf("after %q state=%s out=%q", string(in[i]), w.State(), buf.String())
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if buf.String() != in {
		t.Fatalf("output mismatch:\n got %q\nwant %q", buf.String(), in)
	}
}
