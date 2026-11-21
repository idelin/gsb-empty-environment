package diffdisclose

import (
	"errors"
	"testing"
)

// TestRejectUnderreportedDeletion 删除一条 leafUpdate（删除事件）后，
// 前序结构与根重算必须失败，验证者不得少报删除。
func TestRejectUnderreportedDeletion(t *testing.T) {
	env := newTestEnv(t)
	const height = 3
	oldTree := treeFromMap(t, height, map[uint64]uint64{2: 1, 5: 2})
	newTree := treeFromMap(t, height, map[uint64]uint64{5: 2})
	old, next := env.manifests(t, oldTree, newTree, 1)
	proof, _, err := BuildProof(BuildRequest{
		OldTree: oldTree, NewTree: newTree, Old: old, New: next,
		Authorized: fullRange(height),
	})
	if err != nil {
		t.Fatal(err)
	}
	removed := 0
	for _, rec := range proof.Records {
		if rec.kind == recLeafUpdate && rec.present == sideOld {
			removed++
		}
	}
	if removed == 0 {
		t.Fatalf("test setup: expected a deletion leaf record")
	}

	tampered := *proof
	tampered.Records = make([]record, 0, len(proof.Records))
	for _, rec := range proof.Records {
		if rec.kind == recLeafUpdate && rec.present == sideOld {
			continue // 丢弃删除证据
		}
		tampered.Records = append(tampered.Records, rec)
	}
	dec, derr := DecodeProof(tampered.Encode())
	if derr != nil {
		return // 结构已无法解析，同样是拒绝
	}
	if _, err := dec.Verify(VerifyOptions{Verifier: env.verifier}); err == nil {
		t.Fatalf("proof with dropped deletion must be rejected")
	}
}

// TestRejectEmptyDigestHeightShift 把高度 h 的空摘要挪到另一高度的
// 盲终端，验证者必须按高度绑定拒绝。
func TestRejectEmptyDigestHeightShift(t *testing.T) {
	env := newTestEnv(t)
	const height = 4
	oldTree, _ := NewTree(height, nil)
	newTree := treeFromMap(t, height, map[uint64]uint64{12: 1})
	old, next := env.manifests(t, oldTree, newTree, 1)
	authorized := mustSet(t, Interval{Lo: 0, Hi: 8})
	proof, _, err := BuildProof(BuildRequest{
		OldTree: oldTree, NewTree: newTree, Old: old, New: next,
		Authorized: authorized,
	})
	if err != nil {
		t.Fatal(err)
	}
	// 找到一个区间外盲终端，把其摘要替换成错误高度的空摘要。
	tampered := *proof
	tampered.Records = append([]record(nil), proof.Records...)
	replaced := false
	for i := range tampered.Records {
		if tampered.Records[i].kind == recBlindOutside {
			wrong := mustEmptyDigest(0) // 当前节点高度显然大于 0
			tampered.Records[i].blindOld = wrong
			tampered.Records[i].blindNew = wrong
			replaced = true
			break
		}
	}
	if !replaced {
		t.Fatalf("test setup: expected an outside blind terminal")
	}
	dec, derr := DecodeProof(tampered.Encode())
	if derr != nil {
		t.Fatal(derr)
	}
	if _, err := dec.Verify(VerifyOptions{Verifier: env.verifier}); !errors.Is(err, errDigestHeight) && !errors.Is(err, errRootMismatch) {
		t.Fatalf("height-shifted empty digest must be rejected, got %v", err)
	}
}

// TestRejectMalformedRecordKind 未知记录标签必须在解码阶段拒绝。
func TestRejectMalformedRecordKind(t *testing.T) {
	env := newTestEnv(t)
	const height = 3
	oldTree := treeFromMap(t, height, map[uint64]uint64{1: 1})
	newTree := treeFromMap(t, height, map[uint64]uint64{1: 2})
	old, next := env.manifests(t, oldTree, newTree, 1)
	proof, _, err := BuildProof(BuildRequest{
		OldTree: oldTree, NewTree: newTree, Old: old, New: next,
		Authorized: fullRange(height),
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded := proof.Encode()
	// 记录区从最后一个字节段开始；简单翻转一个记录标签字节（首个记录）。
	tampered := append([]byte(nil), encoded...)
	idx := len(tampered) - totalRecordBytes(proof)
	tampered[idx] = 255
	if _, err := DecodeProof(tampered); !errors.Is(err, errMalformedProof) {
		t.Fatalf("unknown record tag must be rejected, got %v", err)
	}
}

// totalRecordBytes 统计记录段字节长度，辅助定位篡改偏移。
func totalRecordBytes(p *Proof) int {
	clone := *p
	withOne := &Proof{
		OldManifest: clone.OldManifest,
		NewManifest: clone.NewManifest,
		TreeHeight:  clone.TreeHeight,
		Authorized:  clone.Authorized,
		Revoked:     clone.Revoked,
		Cache:       clone.Cache,
		Records:     clone.Records[:len(clone.Records)-1],
	}
	return len(p.Encode()) - len(withOne.Encode())
}

// TestRejectRootWithoutDisclosure 证明编码与摘要不匹配时拒绝。
func TestRejectLeafDigestMismatch(t *testing.T) {
	env := newTestEnv(t)
	const height = 3
	oldTree := treeFromMap(t, height, map[uint64]uint64{1: 1})
	newTree := treeFromMap(t, height, map[uint64]uint64{1: 2})
	old, next := env.manifests(t, oldTree, newTree, 1)
	proof, _, err := BuildProof(BuildRequest{
		OldTree: oldTree, NewTree: newTree, Old: old, New: next,
		Authorized: fullRange(height),
	})
	if err != nil {
		t.Fatal(err)
	}
	tampered := *proof
	tampered.Records = append([]record(nil), proof.Records...)
	for i := range tampered.Records {
		if tampered.Records[i].kind == recLeafUpdate {
			tampered.Records[i].newDigest = mustEmptyDigest(0)
			break
		}
	}
	dec, derr := DecodeProof(tampered.Encode())
	if derr != nil {
		t.Fatal(derr)
	}
	if _, err := dec.Verify(VerifyOptions{Verifier: env.verifier}); err == nil {
		t.Fatalf("leaf digest mismatch must be rejected")
	}
}
