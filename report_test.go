package spandep

import (
	"bytes"
	"strings"
	"testing"
)

func TestRenderReportIsDeterministic(t *testing.T) {
	render := func() string {
		var buf bytes.Buffer
		RenderReport(&buf, Build(Synthesize(1)))
		return buf.String()
	}
	a, b := render(), render()
	if a != b {
		t.Fatalf("report not deterministic:\n%s\n---\n%s", a, b)
	}
	for _, want := range []string{
		"web -> api: calls=2 ok=1 err=1",
		"web -> billing: calls=2 ok=1 err=1",
		"duplicate spans dropped: 1",
		"uncertain evidence (3)",
		"not guessed",
	} {
		if !strings.Contains(a, want) {
			t.Fatalf("report missing %q:\n%s", want, a)
		}
	}
}
