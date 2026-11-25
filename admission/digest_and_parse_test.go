package admission

import (
	"bytes"
	"errors"
	"testing"
)

func TestAdmit_DigestMissingCentralDirectory(t *testing.T) {
	a := assemble(t, true, V2SchemeID)
	layout, err := ParseLayout(a.reader(), int64(len(a.data)))
	if err != nil {
		t.Fatalf("ParseLayout failed: %v", err)
	}

	verifier := &fakeVerifier{want: wantCoverage(a), missingCD: true}
	err = AdmitLayout(layout, 24, verifier.verify)
	if !errors.Is(err, ErrDigestCoverage) {
		t.Fatalf("want ErrDigestCoverage, got %v", err)
	}
	if !verifier.called {
		t.Fatal("digest verifier should have been invoked")
	}

	// 回调返回签名密码学错误同样应映射为摘要/签名拒绝。
	sigErr := errors.New("signature verification failed")
	badSig := &fakeVerifier{want: wantCoverage(a), signatureErr: sigErr}
	if err := AdmitLayout(layout, 24, badSig.verify); !errors.Is(err, ErrDigestCoverage) {
		t.Fatalf("want ErrDigestCoverage for signature failure, got %v", err)
	}
}

func TestAdmit_ValidV2Accepted(t *testing.T) {
	a := assemble(t, true, V2SchemeID)
	layout, err := ParseLayout(a.reader(), int64(len(a.data)))
	if err != nil {
		t.Fatalf("ParseLayout failed: %v", err)
	}

	verifier := &fakeVerifier{want: wantCoverage(a)}
	if err := AdmitLayout(layout, 24, verifier.verify); err != nil {
		t.Fatalf("valid v2 package should be admitted, got %v", err)
	}
	if !verifier.called {
		t.Fatal("digest verifier should have been invoked")
	}

	// 回调收到的三段必须精确：[0,sigStart)、中央目录、EOCD。
	if verifier.got != wantCoverage(a) {
		t.Fatalf("coverage = %+v, want %+v", verifier.got, wantCoverage(a))
	}
	if verifier.got.BeforeSigBlock.End != layout.SigBlock.Start {
		t.Fatal("first digest segment must end right before the signing block")
	}
}

func TestAdmit_ValidV3AndV2V3(t *testing.T) {
	for _, ids := range [][]uint32{{V3SchemeID}, {V2SchemeID, V3SchemeID}} {
		a := assemble(t, true, ids...)
		layout, err := ParseLayout(a.reader(), int64(len(a.data)))
		if err != nil {
			t.Fatalf("ParseLayout failed: %v", err)
		}
		verifier := &fakeVerifier{want: wantCoverage(a)}
		if err := AdmitLayout(layout, 28, verifier.verify); err != nil {
			t.Fatalf("ids %v should be admitted, got %v", ids, err)
		}
		if !verifier.called {
			t.Fatalf("ids %v: verifier not called", ids)
		}
	}
}

func TestAdmit_ModernSignatureWithoutVerifier(t *testing.T) {
	a := assemble(t, true, V2SchemeID)
	layout, err := ParseLayout(a.reader(), int64(len(a.data)))
	if err != nil {
		t.Fatalf("ParseLayout failed: %v", err)
	}
	if err := AdmitLayout(layout, 24, nil); !errors.Is(err, ErrVerifierMissing) {
		t.Fatalf("want ErrVerifierMissing, got %v", err)
	}
}

func TestParse_EOCDCommentOverflow(t *testing.T) {
	a := assemble(t, false)
	tampered := bytes.Clone(a.data)
	// EOCD 注释长度字段位于 EOCD 起始 +20，写入越过 EOF 的伪造长度。
	tampered[a.eocdStart+20] = 0xff
	tampered[a.eocdStart+21] = 0xff

	_, err := ParseLayout(bytes.NewReader(tampered), int64(len(tampered)))
	if !errors.Is(err, ErrEOCDCommentOverflow) {
		t.Fatalf("want ErrEOCDCommentOverflow, got %v", err)
	}
}

func TestParse_Zip64SentinelAndLocatorRejected(t *testing.T) {
	// 中央目录偏移字段写入 0xffffffff 哨兵。
	a := assemble(t, false)
	sentinel := bytes.Clone(a.data)
	cdOffField := a.eocdStart + 16 // EOCD: sig(4)+disk(2)+cdDisk(2)+entries(2)+total(2)+cdSize(4)
	for i := 0; i < 4; i++ {
		sentinel[cdOffField+int64(i)] = 0xff
	}
	if _, err := ParseLayout(bytes.NewReader(sentinel), int64(len(sentinel))); !errors.Is(err, ErrZip64) {
		t.Fatalf("want ErrZip64 for sentinel, got %v", err)
	}

	// 在中央目录与 EOCD 之间插入 Zip64 EOCD Locator，并重写 EOCD 的
	// 中央目录偏移，保证除 Zip64 标记外其余结构自洽。
	locator := make([]byte, 0, zip64LocatorSize)
	locator = append(locator, zip64EOCDLocatorSignature...)
	locator = append(locator, make([]byte, 16)...)

	cdSize := uint32(a.eocdStart - a.cdStart)
	newCDOffset := uint32(a.cdStart) // CD 本体位置不变，其后紧跟 locator
	newEOCD := buildEOCD(newCDOffset, cdSize, 1, nil)

	var rebuilt bytes.Buffer
	rebuilt.Write(a.data[:a.eocdStart]) // 本地条目 + 签名分块（若有）+ 中央目录
	rebuilt.Write(locator)
	rebuilt.Write(newEOCD)
	if _, err := ParseLayout(bytes.NewReader(rebuilt.Bytes()), int64(rebuilt.Len())); !errors.Is(err, ErrZip64) {
		t.Fatalf("want ErrZip64 for locator, got %v", err)
	}
}

func TestAdmit_SigBlockNotAdjacent(t *testing.T) {
	a := assemble(t, true, V2SchemeID)
	layout, err := ParseLayout(a.reader(), int64(len(a.data)))
	if err != nil {
		t.Fatalf("ParseLayout failed: %v", err)
	}
	// 人为让签名分块起点与中央目录起点不再相邻；同步保持长度字段关系合法，
	// 以便隔离出「不相邻」这一判定。
	layout.SigBlock.Start = layout.CentralDirectory.Start - 4
	layout.SigBlock.SizeOfBlockBefore = layout.SigBlock.SizeOfBlockAfter - 8 // 保持 after=before+8
	verifier := &fakeVerifier{want: wantCoverage(a)}
	err = AdmitLayout(layout, 24, verifier.verify)
	if !errors.Is(err, ErrSigBlockNotAdjacent) {
		t.Fatalf("want ErrSigBlockNotAdjacent, got %v", err)
	}
	if verifier.called {
		t.Fatal("verifier must not run when structural checks already failed")
	}
}

func TestParse_NotZipAndTruncated(t *testing.T) {
	if _, err := ParseLayout(bytes.NewReader([]byte("short")), 5); !errors.Is(err, ErrNotZip) {
		t.Fatalf("want ErrNotZip, got %v", err)
	}
	a := assemble(t, false)
	truncated := a.data[:a.eocdStart] // 丢掉 EOCD
	if _, err := ParseLayout(bytes.NewReader(truncated), int64(len(truncated))); !errors.Is(err, ErrEOCD) {
		t.Fatalf("want ErrEOCD, got %v", err)
	}
}
