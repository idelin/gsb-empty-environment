package netsec

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
)

type elfSec struct {
	name    string
	data    []byte
	off     uint64
	size    uint64
	noPlace bool
}

func buildELF(t *testing.T, secs []elfSec) []byte {
	t.Helper()
	shstr := "\x00"
	nameOff := map[string]uint32{}
	for _, s := range secs {
		nameOff[s.name] = uint32(len(shstr))
		shstr += s.name + "\x00"
	}
	nameOff[".shstrtab"] = uint32(len(shstr))
	shstr += ".shstrtab\x00"
	const ehsize = 64
	body := make([]byte, ehsize)
	offs := make([]uint64, len(secs))
	sizes := make([]uint64, len(secs))
	for i, s := range secs {
		off := s.off
		if off == 0 {
			off = uint64(len(body))
		}
		sz := s.size
		if sz == 0 {
			sz = uint64(len(s.data))
		}
		offs[i], sizes[i] = off, sz
		if s.noPlace {
			continue
		}
		end := int(off) + len(s.data)
		if end > len(body) {
			body = append(body, make([]byte, end-len(body))...)
		}
		copy(body[off:], s.data)
	}
	shstrOff := uint64(len(body))
	body = append(body, shstr...)
	shoff := uint64(len(body))
	shnum := len(secs) + 2
	sh := make([]byte, shnum*64)
	putSh := func(idx int, name uint32, shtype uint32, off, size uint64) {
		b := sh[idx*64:]
		binary.LittleEndian.PutUint32(b[0:], name)
		binary.LittleEndian.PutUint32(b[4:], shtype)
		binary.LittleEndian.PutUint64(b[24:], off)
		binary.LittleEndian.PutUint64(b[32:], size)
	}
	for i, s := range secs {
		putSh(i+1, nameOff[s.name], 1, offs[i], sizes[i])
	}
	putSh(shnum-1, nameOff[".shstrtab"], 3, shstrOff, uint64(len(shstr)))
	body = append(body, sh...)
	hdr := body[:ehsize]
	copy(hdr, []byte{0x7f, 'E', 'L', 'F', 2, 1, 1, 0})
	binary.LittleEndian.PutUint16(hdr[16:], 2)
	binary.LittleEndian.PutUint16(hdr[18:], 0x3e)
	binary.LittleEndian.PutUint32(hdr[20:], 1)
	binary.LittleEndian.PutUint64(hdr[40:], shoff)
	binary.LittleEndian.PutUint16(hdr[52:], ehsize)
	binary.LittleEndian.PutUint16(hdr[58:], 64)
	binary.LittleEndian.PutUint16(hdr[60:], uint16(shnum))
	binary.LittleEndian.PutUint16(hdr[62:], uint16(shnum-1))
	return body
}

func genKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

const testInfo = "gh-releases-zsync|owner|repo|latest|myapp-*.AppImage"

func signedImage(t *testing.T, embedPub ed25519.PublicKey, priv ed25519.PrivateKey, info string) []byte {
	t.Helper()
	secs := []elfSec{
		{name: SectionUpdateInfo, data: []byte(info)},
		{name: SectionSig, data: make([]byte, ed25519.SignatureSize)},
		{name: SectionKey, data: append([]byte(embedPub), 0, 0)},
	}
	img := buildELF(t, secs)
	s, err := FindSections(img)
	if err != nil {
		t.Fatal(err)
	}
	digest := Digest(img, s)
	secs[1].data = ed25519.Sign(priv, digest[:])
	return buildELF(t, secs)
}

func TestVerifyUpdateOK(t *testing.T) {
	pub, priv := genKey(t)
	current := signedImage(t, pub, priv, testInfo)
	candidate := signedImage(t, pub, priv, testInfo)
	info, err := VerifyUpdate(current, candidate)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if info.Owner != "owner" || info.Repo != "repo" || info.Tag != "latest" {
		t.Fatalf("unexpected info: %+v", info)
	}
	dl, err := info.DownloadURL("myapp-1.2.3.AppImage")
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckRemoteURL(dl); err != nil {
		t.Fatalf("download url not https: %v", err)
	}
	zs, err := info.ZsyncURL("myapp-1.2.3.AppImage")
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckRemoteURL(zs); err != nil {
		t.Fatalf("zsync url not https: %v", err)
	}
	if !strings.HasSuffix(zs, ".zsync") {
		t.Fatalf("bad zsync url %q", zs)
	}
}

func TestVerifyUpdateKeySwap(t *testing.T) {
	pubA, privA := genKey(t)
	pubB, privB := genKey(t)
	current := signedImage(t, pubA, privA, testInfo)
	candidate := signedImage(t, pubB, privB, testInfo)
	if _, err := VerifyUpdate(current, candidate); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("want ErrBadSignature, got %v", err)
	}
}

func TestFindSectionsOverlap(t *testing.T) {
	secs := []elfSec{
		{name: SectionUpdateInfo, data: []byte(testInfo)},
		{name: SectionSig, data: make([]byte, 64)},
		{name: SectionKey, data: make([]byte, 32)},
	}
	img := buildELF(t, secs)
	s, err := FindSections(img)
	if err != nil {
		t.Fatal(err)
	}
	secs[2].off = s.Sig.Offset + 8
	overlap := buildELF(t, secs)
	if _, err := FindSections(overlap); !errors.Is(err, ErrSectionOverlap) {
		t.Fatalf("want ErrSectionOverlap, got %v", err)
	}
}

func TestFindSectionsOutOfBounds(t *testing.T) {
	secs := []elfSec{
		{name: SectionUpdateInfo, data: []byte(testInfo)},
		{name: SectionSig, data: make([]byte, 64), size: 1 << 40},
		{name: SectionKey, data: make([]byte, 32)},
	}
	if _, err := FindSections(buildELF(t, secs)); !errors.Is(err, ErrSectionBounds) {
		t.Fatalf("want ErrSectionBounds, got %v", err)
	}
	secs[1] = elfSec{name: SectionSig, off: 1 << 40, size: 64, noPlace: true}
	if _, err := FindSections(buildELF(t, secs)); !errors.Is(err, ErrSectionBounds) {
		t.Fatalf("want ErrSectionBounds, got %v", err)
	}
}

func TestCheckRemoteURL(t *testing.T) {
	for _, u := range []string{
		"http://github.com/o/r/releases/latest/download/a.AppImage",
		"http://example.com/a.AppImage.zsync",
		"ftp://example.com/a",
		"//example.com/a",
	} {
		if err := CheckRemoteURL(u); !errors.Is(err, ErrNotHTTPS) {
			t.Fatalf("%q: want ErrNotHTTPS, got %v", u, err)
		}
	}
	if err := CheckRemoteURL("https://github.com/o/r/releases/latest/download/a.AppImage"); err != nil {
		t.Fatalf("https rejected: %v", err)
	}
}

func TestGlobMatch(t *testing.T) {
	info, err := ParseUpdateInfo(testInfo)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		want bool
	}{
		{"myapp-1.0.AppImage", true},
		{"myapp-.AppImage", true},
		{"myapp-1.0.AppImage.zsync", false},
		{"xmyapp-1.0.AppImage", false},
		{"myapp-a/b.AppImage", false},
	}
	for _, c := range cases {
		if got := info.Match(c.name); got != c.want {
			t.Errorf("Match(%q) = %v, want %v", c.name, got, c.want)
		}
	}
	q, err := ParseUpdateInfo("gh-releases-zsync|o|r|latest|app-?.zip")
	if err != nil {
		t.Fatal(err)
	}
	if !q.Match("app-1.zip") || q.Match("app-12.zip") || q.Match("app-/.zip") {
		t.Errorf("? wildcard misbehaves")
	}
}

func TestParseUpdateInfoBad(t *testing.T) {
	for _, raw := range []string{
		"gh-releases-zsync|owner|repo|latest",
		"http|owner|repo|latest|*.AppImage",
		"gh-releases-zsync|ow/ner|repo|latest|*.AppImage",
		"gh-releases-zsync|owner|repo|latest|a/b.AppImage",
		"gh-releases-zsync|owner|repo|../x|*.AppImage",
	} {
		if _, err := ParseUpdateInfo(raw); err == nil {
			t.Errorf("%q: want error", raw)
		}
	}
}
