package admission

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

// assemble 拼装一个带可选签名分块的完整安装包。
func assemble(t *testing.T, withSigBlock bool, sigIDs ...uint32) *archive {
	t.Helper()
	name := "classes.dex"
	payload := bytes.Repeat([]byte{0xAB}, 16)
	local := buildLocalEntry(name, payload)

	var pairs [][]byte
	for _, id := range sigIDs {
		pairs = append(pairs, sigPair(id, []byte("sig-"+name)))
	}
	sigBlock, sizeOfBlock := buildSigBlock(pairs...)

	localStart := int64(0)
	sigStart := int64(len(local))
	cdStart := sigStart
	if withSigBlock {
		cdStart = sigStart + int64(len(sigBlock))
	}
	central := buildCentralEntry(name, payload, 0)
	cdSize := uint32(len(central))
	eocd := buildEOCD(uint32(cdStart), cdSize, 1, nil)

	var data bytes.Buffer
	data.Write(local)
	if withSigBlock {
		data.Write(sigBlock)
	}
	data.Write(central)
	eocdStart := int64(data.Len())
	data.Write(eocd)

	return &archive{
		data:             data.Bytes(),
		localStart:       localStart,
		cdStart:          cdStart,
		eocdStart:        eocdStart,
		sigStart:         sigStart,
		sigSizeOfBlock:   sizeOfBlock,
		firstLocalOffset: 0,
	}
}

func (a *archive) reader() *bytes.Reader { return bytes.NewReader(a.data) }

func (a *archive) input(minSDK int, verify DigestVerifier) Input {
	first8 := make([]byte, 8)
	copy(first8, a.data)
	return Input{
		First8:                 first8,
		CentralDirectory:       Range{Start: a.cdStart, End: a.eocdStart},
		EOCD:                   Range{Start: a.eocdStart, End: int64(len(a.data))},
		FirstLocalHeaderOffset: int64(a.firstLocalOffset),
		MinSDKVersion:          minSDK,
		VerifyDigest:           verify,
	}
}

func wantCoverage(a *archive) DigestCoverage {
	return DigestCoverage{
		BeforeSigBlock:   Range{Start: 0, End: a.sigStart},
		CentralDirectory: Range{Start: a.cdStart, End: a.eocdStart},
		EOCD:             Range{Start: a.eocdStart, End: int64(len(a.data))},
	}
}

func TestParseLayout_ValidV2(t *testing.T) {
	a := assemble(t, true, V2SchemeID)
	layout, err := ParseLayout(a.reader(), int64(len(a.data)))
	if err != nil {
		t.Fatalf("ParseLayout failed: %v", err)
	}
	if !layout.SigBlock.Present || !layout.SigBlock.HasV2 || layout.SigBlock.HasV3 {
		t.Fatalf("unexpected sig block: %+v", layout.SigBlock)
	}
	if layout.SigBlock.Start != a.sigStart {
		t.Fatalf("sig start = %d, want %d", layout.SigBlock.Start, a.sigStart)
	}
	if layout.FirstLocalHeaderOffset != 0 {
		t.Fatalf("first local offset = %d, want 0", layout.FirstLocalHeaderOffset)
	}
	if layout.CentralDirectory.Start != a.cdStart || layout.EOCD.Start != a.eocdStart {
		t.Fatalf("ranges mismatch: %+v", layout)
	}
}

func TestAdmit_DexPrefixPolymorphic(t *testing.T) {
	a := assemble(t, true, V2SchemeID)
	dexHeader := []byte("dex\n035\x00")
	prefixed := append(append([]byte{}, dexHeader...), a.data...)
	shift := int64(len(dexHeader))
	// 模拟真实 Janus 手法：保持 ZIP 内部偏移自洽，仅整体后移，
	// 重写 EOCD 中的中央目录偏移（CD 大小与 EOCD 位置不变）。
	newCDOff := uint32(int64(a.cdStart) + shift)
	cdSize := uint32(a.eocdStart - a.cdStart)
	newEOCD := buildEOCD(newCDOff, cdSize, 1, nil)
	copy(prefixed[int64(len(prefixed))-int64(len(newEOCD)):], newEOCD)

	// 解析器视角：EOCD 仍在文件末尾，ZIP 结构依旧合法，体现多态文件的危害。
	layout, err := ParseLayout(bytes.NewReader(prefixed), int64(len(prefixed)))
	if err != nil {
		t.Fatalf("ParseLayout should still see a valid zip, got %v", err)
	}
	if !bytes.HasPrefix(layout.First8, dexMagic) {
		t.Fatalf("expected dex magic prefix, got % x", layout.First8)
	}
	if err := AdmitLayout(layout, 24, (&fakeVerifier{}).verify); !errors.Is(err, ErrPolymorphic) {
		t.Fatalf("want ErrPolymorphic, got %v", err)
	}
}

func TestAdmit_NonZipHeaderWithValidEOCD(t *testing.T) {
	a := assemble(t, false)
	corrupted := bytes.Clone(a.data)
	corrupted[0] = 0x00 // 破坏本地头魔数但保留合法 EOCD
	corrupted[1] = 0x00

	layout, err := ParseLayout(bytes.NewReader(corrupted), int64(len(corrupted)))
	if err != nil {
		t.Fatalf("ParseLayout failed: %v", err)
	}
	if err := AdmitLayout(layout, 21, nil); !errors.Is(err, ErrPolymorphic) {
		t.Fatalf("want ErrPolymorphic for non-zip header, got %v", err)
	}
}

func TestAdmit_LocalHeaderNotAtZero(t *testing.T) {
	a := assemble(t, false)
	in := a.input(21, nil)
	in.FirstLocalHeaderOffset = 64
	if err := Admit(in); !errors.Is(err, ErrLocalHeaderNotAtZero) {
		t.Fatalf("want ErrLocalHeaderNotAtZero, got %v", err)
	}

	// 字节级：本地头前移 64 字节，中央目录指向非零偏移，也必须被拦截。
	prefix := bytes.Repeat([]byte{0xEE}, 64)
	name := "classes.dex"
	payload := bytes.Repeat([]byte{0xAB}, 16)
	shifted := append(prefix, buildLocalEntry(name, payload)...)
	central := buildCentralEntry(name, payload, uint32(len(prefix)))
	cdStart := uint32(len(shifted))
	eocd := buildEOCD(cdStart, uint32(len(central)), 1, nil)
	full := append(append(shifted, central...), eocd...)

	layout, err := ParseLayout(bytes.NewReader(full), int64(len(full)))
	if err != nil {
		t.Fatalf("ParseLayout failed: %v", err)
	}
	if err := AdmitLayout(layout, 21, nil); !errors.Is(err, ErrLocalHeaderNotAtZero) {
		t.Fatalf("want ErrLocalHeaderNotAtZero for shifted header, got %v", err)
	}
}

func TestAdmit_V1OnlyRejectedAtSDK24AcceptedBelow24(t *testing.T) {
	a := assemble(t, false) // 无签名分块，等价于仅 v1

	if err := Admit(a.input(24, nil)); !errors.Is(err, ErrUnsupportedSignature) {
		t.Fatalf("want ErrUnsupportedSignature at SDK 24, got %v", err)
	}
	if err := Admit(a.input(23, nil)); err != nil {
		t.Fatalf("v1-only should be accepted below SDK 24, got %v", err)
	}
}

func TestAdmit_SigBlockSizeMismatch(t *testing.T) {
	a := assemble(t, true, V2SchemeID)

	layout, err := ParseLayout(a.reader(), int64(len(a.data)))
	if err != nil {
		t.Fatalf("ParseLayout failed: %v", err)
	}
	layout.SigBlock.SizeOfBlockBefore = layout.SigBlock.SizeOfBlockAfter // 破坏 after=before+8
	if err := AdmitLayout(layout, 24, (&fakeVerifier{}).verify); !errors.Is(err, ErrSigBlockSizeMismatch) {
		t.Fatalf("want ErrSigBlockSizeMismatch, got %v", err)
	}

	// 字节级篡改：只改头部长度字段，块起点（由尾部字段定位）不变，
	// 因此长度不一致会先于摘要覆盖被触发。
	tampered := bytes.Clone(a.data)
	binary.LittleEndian.PutUint64(tampered[a.sigStart:a.sigStart+8], a.sigSizeOfBlock+16) // before 被改大
	tamperedLayout, perr := ParseLayout(bytes.NewReader(tampered), int64(len(tampered)))
	if perr != nil {
		t.Fatalf("ParseLayout failed: %v", perr)
	}
	err = AdmitLayout(tamperedLayout, 24, (&fakeVerifier{}).verify)
	if !errors.Is(err, ErrSigBlockSizeMismatch) {
		t.Fatalf("want ErrSigBlockSizeMismatch on byte tamper, got %v", err)
	}
}
