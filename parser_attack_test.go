package streamguard_test

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	sg "formal.local/w27-8ca5752422"
)

func drainParser(p *sg.Parser) error {
	for {
		_, err := p.Next()
		if errors.Is(err, sg.ErrNeedMore) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func TestParserRejectsBillionLaughsDTD(t *testing.T) {
	payload := `<?xml version="1.0"?>
<!DOCTYPE lolz [
  <!ENTITY lol "lol">
  <!ENTITY lol2 "&lol;&lol;&lol;&lol;&lol;&lol;&lol;&lol;&lol;&lol;">
  <!ENTITY lol3 "&lol2;&lol2;&lol2;&lol2;&lol2;&lol2;&lol2;&lol2;&lol2;&lol2;">
  <!ENTITY lol4 "&lol3;&lol3;&lol3;&lol3;&lol3;&lol3;&lol3;&lol3;&lol3;&lol3;">
]>
<lolz>&lol4;</lolz>`

	parser := sg.NewParser(sg.DefaultLimits())
	if _, err := parser.Write([]byte(payload)); err != nil {
		t.Fatalf("Write: unexpected immediate error: %v", err)
	}

	err := drainParser(parser)
	if !errors.Is(err, sg.ErrDTDForbidden) {
		t.Fatalf("expected ErrDTDForbidden, got %v", err)
	}
}

func TestParserRejectsEntityDeclarationSplitAcrossWrites(t *testing.T) {
	payload := []byte(`<!ENTITY billion "lol-lol-lol">`)
	parser := sg.NewParser(sg.DefaultLimits())

	var attackErr error
	for i := range payload {
		if _, err := parser.Write(payload[i : i+1]); err != nil {
			attackErr = err
			break
		}
		for {
			_, err := parser.Next()
			if errors.Is(err, sg.ErrNeedMore) {
				break
			}
			if err != nil {
				attackErr = err
				break
			}
		}
		if attackErr != nil {
			break
		}
	}

	if !errors.Is(attackErr, sg.ErrEntityDeclForbidden) {
		t.Fatalf("expected split ENTITY declaration to fail with ErrEntityDeclForbidden, got %v", attackErr)
	}
}

func TestParserRejectsUndeclaredCustomEntityReference(t *testing.T) {
	parser := sg.NewParser(sg.DefaultLimits())
	_, _ = parser.Write([]byte(`<root>&expand-me;</root>`))

	err := drainParser(parser)
	if !errors.Is(err, sg.ErrUndeclaredEntity) && !errors.Is(err, sg.ErrEntityDeclForbidden) {
		t.Fatalf("expected custom entity reference rejection, got %v", err)
	}
}

func TestParserDecodesOnlySafeBuiltinEntities(t *testing.T) {
	reader := sg.NewFrameReader(sg.DefaultLimits())
	_, err := reader.Write([]byte(`<stream><text>a&amp;b&lt;c&gt;d&quot;e&apos;f&#65;</text></stream>`))
	if err != nil {
		t.Fatal(err)
	}

	open, err := reader.NextFrame()
	if err != nil || open.Kind != sg.FrameStreamOpen {
		t.Fatalf("stream open: frame=%+v err=%v", open, err)
	}
	frame, err := reader.NextFrame()
	if err != nil {
		t.Fatal(err)
	}
	if frame.Element == nil || frame.Element.Text != `a&b<c>d"e'fA` {
		t.Fatalf("unexpected decoded text: %q", frame.Element.Text)
	}
}

func TestParserRejectsCommentBomb(t *testing.T) {
	limits := sg.DefaultLimits()
	comment := "<!--" + strings.Repeat("x", limits.MaxCommentBytes+1) + "-->"
	payload := "<stream:stream>" + comment + "</stream:stream>"

	guard := sg.NewGuard(sg.Config{Limits: limits})
	_, err := guard.Feed([]byte(payload))
	if !errors.Is(err, sg.ErrCommentBomb) {
		t.Fatalf("expected ErrCommentBomb, got %v", err)
	}
}

func TestParserRejectsTooManyCommentsInFrame(t *testing.T) {
	limits := sg.DefaultLimits()
	var comments strings.Builder
	for i := 0; i < limits.MaxCommentsPerFrame+1; i++ {
		comments.WriteString("<!--x-->")
	}
	payload := "<stream:stream>" + comments.String() + "<message/></stream:stream>"

	guard := sg.NewGuard(sg.Config{Limits: limits})
	_, err := guard.Feed([]byte(payload))
	if !errors.Is(err, sg.ErrCommentBomb) {
		t.Fatalf("expected repeated comment bomb error, got %v", err)
	}
}

func TestParserRejectsExcessiveDepth(t *testing.T) {
	limits := sg.DefaultLimits()
	payload := []byte(strings.Repeat("<n>", limits.MaxDepth+1))

	parser := sg.NewParser(limits)
	_, _ = parser.Write(payload)
	err := drainParser(parser)
	if !errors.Is(err, sg.ErrDepthExceeded) {
		t.Fatalf("expected ErrDepthExceeded, got %v", err)
	}
}

func TestParserRejectsTooManyAttributes(t *testing.T) {
	limits := sg.DefaultLimits()
	var attrs strings.Builder
	for i := 0; i < limits.MaxAttributes+1; i++ {
		attrs.WriteString(` a`)
		attrs.WriteString(strconv.Itoa(i))
		attrs.WriteString(`="v"`)
	}
	payload := []byte("<n" + attrs.String() + "/>")

	parser := sg.NewParser(limits)
	_, _ = parser.Write(payload)
	err := drainParser(parser)
	if !errors.Is(err, sg.ErrAttributeLimit) {
		t.Fatalf("expected ErrAttributeLimit, got %v", err)
	}
}

func TestParserRejectsInvalidUTF8(t *testing.T) {
	parser := sg.NewParser(sg.DefaultLimits())
	_, err := parser.Write([]byte("<n>\xff</n>"))
	if !errors.Is(err, sg.ErrInvalidUTF8) {
		t.Fatalf("expected ErrInvalidUTF8 on Write, got %v", err)
	}
}

func TestParserAcceptsMultibyteUTF8SplitAcrossWrites(t *testing.T) {
	reader := sg.NewFrameReader(sg.DefaultLimits())
	payload := []byte(`<stream><body>阀门☃</body></stream>`)

	var frame *sg.Frame
	for i := range payload {
		if _, err := reader.Write(payload[i : i+1]); err != nil {
			t.Fatalf("byte %d: %v", i, err)
		}
		for {
			candidate, err := reader.NextFrame()
			if errors.Is(err, sg.ErrNeedMore) {
				break
			}
			if err != nil {
				t.Fatalf("byte %d: parse: %v", i, err)
			}
			if candidate.Kind == sg.FrameStanza {
				frame = candidate
			}
		}
	}

	if frame == nil {
		t.Fatal("expected body frame")
	}
	if frame.Element.Text != "阀门☃" {
		t.Fatalf("unexpected text %q", frame.Element.Text)
	}
}
