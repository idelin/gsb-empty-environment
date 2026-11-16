package netsec

import (
	"bytes"
	"errors"
	"io"
	"math/rand"
	"reflect"
	"testing"
)

func TestChunkingInvariance(t *testing.T) {
	cases := map[string][]byte{
		"single frame": frame("MSH|^~\\&|A|B|||20261116|||ADT^A01|1|P|2.5" + string(CR)),
		"two frames": bytes.Join([][]byte{
			frame("MSH|...|A" + string(CR)),
			frame("PID|1||123" + string(CR)),
		}, nil),
		"empty frame":                   {VT, FS, CR},
		"bad trailer then resync at VT": append(append([]byte{VT}, 'a', 'b', FS, 'X'), frame("ok")...),
		"bad trailer where FS meets VT": append(append([]byte{VT}, 'a', 'b', FS), frame("ok")...),
		"second VT inside frame fatal":  append(append([]byte{VT}, 'a', 'b', VT), frame("ok")...),
		"junk byte outside frame fatal": append([]byte{'X'}, frame("ok")...),
		"oversized then next legal frame": bytes.Join([][]byte{
			{VT}, []byte("abcdefgh"), {FS, CR}, frame("ok"),
		}, nil),
		"frame exactly at limit": frame("abc"),
		"truncated inside frame": {VT, 'a', 'b', 'c'},
		"truncated after FS":     {VT, 'a', 'b', 'c', FS},
		"soft error between good": bytes.Join([][]byte{
			frame("one"), {VT, FS, 'X'}, frame("two"),
		}, nil),
	}

	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			want := runParser([][]byte{append([]byte(nil), data...)}, 3)
			for idx, chunks := range everyChunking(t, data) {
				got := runParser(chunks, 3)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("chunking #%d %v:\n got %#v\nwant %#v", idx, chunkLengths(chunks), got, want)
				}
			}
		})
	}
}

func TestEmbeddedTrailerAttack(t *testing.T) {
	malicious := []byte{VT}
	malicious = append(malicious, []byte("MSH|^~\\&|evil")...)
	malicious = append(malicious, FS, CR)
	malicious = append(malicious, []byte("PID|injected tail")...)
	malicious = append(malicious, FS, CR)

	p := NewParser(0)
	for _, chunks := range everyChunking(t, malicious) {
		p.Reset()
		var sawInjected bool
		for _, c := range chunks {
			_ = p.Feed(c)
		}
		for {
			fr, err := p.Next()
			if fr == nil && err == nil {
				break
			}
			if err != nil {
				if !errors.Is(err, ErrProtocol) {
					t.Fatalf("expected ErrProtocol for smuggled frame boundary, got %v", err)
				}
				break
			}
			if bytes.Contains(fr, []byte("PID|injected tail")) {
				sawInjected = true
			}
		}
		if sawInjected {
			t.Fatal("injected tail was delivered as a standalone frame")
		}
	}

	field := []byte(`note \X1C\0D\X0B\ tail`)
	if _, err := UnescapeField(field); !errors.Is(err, ErrEscape) {
		t.Fatalf("escaped framing bytes must be rejected, got %v", err)
	}
}

func TestSplitTrailerEveryPosition(t *testing.T) {
	data := frame("hello")
	want := runParser([][]byte{append([]byte(nil), data...)}, 0)
	fsPos := bytes.IndexByte(data, FS)
	if data[fsPos+1] != CR {
		t.Fatal("test setup error")
	}
	for k := 0; k <= len(data); k++ {
		var chunks [][]byte
		if k == 0 || k == len(data) {
			chunks = [][]byte{append([]byte(nil), data...)}
		} else {
			chunks = [][]byte{
				append([]byte(nil), data[:k]...),
				append([]byte(nil), data[k:]...),
			}
		}
		got := runParser(chunks, 0)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("cut at %d:\n got %#v\nwant %#v", k, got, want)
		}
	}
}

func TestOversizedFrameSkipsToNextStart(t *testing.T) {
	stream := bytes.Join([][]byte{{VT}, []byte("toolongpayload"), {FS, CR}, frame("next")}, nil)
	events := runParser([][]byte{stream}, 4)

	var gotFrames [][]byte
	var sawTooLarge, sawProtocol bool
	for _, ev := range events {
		switch {
		case ev.kind == "frame":
			gotFrames = append(gotFrames, ev.frame)
		case errors.Is(ev.sentinel, ErrFrameTooLarge):
			sawTooLarge = true
		case errors.Is(ev.sentinel, ErrProtocol):
			sawProtocol = true
		}
	}
	if !sawTooLarge {
		t.Fatal("missing ErrFrameTooLarge")
	}
	if sawProtocol {
		t.Fatal("resync after oversized frame must not raise a protocol error")
	}
	if len(gotFrames) != 1 || string(gotFrames[0]) != "next" {
		t.Fatalf("want only frame %q after resync, got %q", "next", gotFrames)
	}

	exact := runParser([][]byte{frame("abcd")}, 4)
	if len(exact) != 2 || exact[0].kind != "frame" || string(exact[0].frame) != "abcd" {
		t.Fatalf("limit-sized frame rejected: %#v", exact)
	}
}

func TestRandomStreamsMatchAcrossChunkings(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	alphabet := []byte{VT, FS, CR, 'a', 'b', 'X', '|', '^'}
	for trial := 0; trial < 300; trial++ {
		n := 1 + rng.Intn(18)
		data := make([]byte, n)
		for i := range data {
			data[i] = alphabet[rng.Intn(len(alphabet))]
		}
		limit := []int{0, 1, 3, 8}[rng.Intn(4)]
		want := runParser([][]byte{append([]byte(nil), data...)}, limit)
		for _, chunks := range everyChunking(t, data) {
			got := runParser(chunks, limit)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("stream % X chunking %v:\n got %#v\nwant %#v",
					data, chunkLengths(chunks), got, want)
			}
		}
	}
}

type oneByteReader struct {
	data []byte
	pos  int
}

func (r *oneByteReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	p[0] = r.data[r.pos]
	r.pos++
	return 1, nil
}

func TestFrameReaderDeliversFramesAndRecovers(t *testing.T) {
	stream := bytes.Join([][]byte{
		frame("one"),
		{VT, FS, 'X'},
		frame("two"),
	}, nil)
	fr := NewFrameReaderWithChunkSize(&oneByteReader{data: stream}, 0, 1)

	first, err := fr.ReadFrame()
	if err != nil || string(first) != "one" {
		t.Fatalf("first frame = %q, %v", first, err)
	}
	if _, err = fr.ReadFrame(); !errors.Is(err, ErrBadTrailer) {
		t.Fatalf("want ErrBadTrailer, got %v", err)
	}
	second, err := fr.ReadFrame()
	if err != nil || string(second) != "two" {
		t.Fatalf("second frame = %q, %v", second, err)
	}
	if _, err = fr.ReadFrame(); !errors.Is(err, io.EOF) {
		t.Fatalf("want io.EOF on clean end, got %v", err)
	}
}

func TestFrameReaderTruncatedAndFatal(t *testing.T) {
	fr := NewFrameReader(&oneByteReader{data: []byte{VT, 'a', 'b'}}, 0)
	if _, err := fr.ReadFrame(); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("want ErrUnexpectedEOF, got %v", err)
	}

	fatal := NewFrameReader(&oneByteReader{data: append([]byte{'J', 'U', 'N', 'K'}, frame("x")...)}, 0)
	if _, err := fatal.ReadFrame(); !errors.Is(err, ErrProtocol) {
		t.Fatalf("want ErrProtocol, got %v", err)
	}
	if _, err := fatal.ReadFrame(); !errors.Is(err, ErrProtocol) {
		t.Fatalf("fatal error must be sticky, got %v", err)
	}
}
