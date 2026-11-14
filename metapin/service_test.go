package metapin_test

import (
	"errors"
	"fmt"
	"testing"

	"example.org/netsec/q0454/metapin"
)

// host simulates the mutable external hosting the collectible contract
// points at. Attackers can replace content or hijack locations at will.
type host struct {
	files map[string][]byte
}

func newHost() *host { return &host{files: make(map[string][]byte)} }

func (h *host) put(uri string, data []byte) { h.files[uri] = data }

func (h *host) Fetch(uri string) ([]byte, error) {
	data, ok := h.files[uri]
	if !ok {
		return nil, fmt.Errorf("404: %s", uri)
	}
	return data, nil
}

var (
	original = metapin.Metadata{
		Name:  "Genesis #1",
		Image: "https://cdn.example/img/1.png",
		Attributes: []metapin.Attribute{
			{TraitType: "rarity", Value: "legendary"},
		},
	}
	// attacker rewrite: same token, different image and traits.
	defaced = metapin.Metadata{
		Name:  "Genesis #1",
		Image: "https://evil.example/img/rug.png",
		Attributes: []metapin.Attribute{
			{TraitType: "rarity", Value: "common"},
		},
	}
)

func mustRaw(t *testing.T, md metapin.Metadata) []byte {
	t.Helper()
	raw, err := metapin.CanonicalBytes(md)
	if err != nil {
		t.Fatalf("canonicalize: %v", err)
	}
	return raw
}

func setup(t *testing.T) (*metapin.Service, *host, metapin.Digest) {
	t.Helper()
	svc := metapin.NewService(metapin.NewInMemoryLedger())
	h := newHost()
	h.put("https://hosting.example/meta/1.json", mustRaw(t, original))
	digest, err := svc.Mint("token-1", "alice", original, "https://hosting.example/meta/1.json")
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	return svc, h, digest
}

// 合法展示：托管内容未被改动时正常展示。
func TestLegitimateDisplay(t *testing.T) {
	svc, h, _ := setup(t)
	md, err := svc.Display("token-1", h)
	if err != nil {
		t.Fatalf("display: %v", err)
	}
	if md.Image != original.Image || md.Attributes[0].Value != "legendary" {
		t.Fatalf("unexpected metadata: %+v", md)
	}
}

// 内容替换：攻击者改写托管地址上的图片与属性，展示被拒绝。
func TestContentReplacementRefused(t *testing.T) {
	svc, h, _ := setup(t)
	h.put("https://hosting.example/meta/1.json", mustRaw(t, defaced))
	_, err := svc.Display("token-1", h)
	if !errors.Is(err, metapin.ErrDigestMismatch) {
		t.Fatalf("want ErrDigestMismatch, got %v", err)
	}
}

// 地址改指：把检索地址指向攻击者位置，只有内容仍匹配承诺才接受。
func TestAddressRepointing(t *testing.T) {
	svc, h, digest := setup(t)

	// 攻击者位置托管篡改内容，改指被拒绝，原地址不受影响。
	h.put("https://evil.example/meta/1.json", mustRaw(t, defaced))
	if err := svc.UpdateRetrievalURI("token-1", "https://evil.example/meta/1.json", h); !errors.Is(err, metapin.ErrDigestMismatch) {
		t.Fatalf("want ErrDigestMismatch, got %v", err)
	}
	if _, err := svc.Display("token-1", h); err != nil {
		t.Fatalf("display after rejected repoint: %v", err)
	}

	// 合法迁移：新位置托管与承诺一致的内容，改指成功且承诺不变。
	h.put("https://mirror.example/meta/1.json", mustRaw(t, original))
	if err := svc.UpdateRetrievalURI("token-1", "https://mirror.example/meta/1.json", h); err != nil {
		t.Fatalf("legit repoint: %v", err)
	}
	md, err := svc.Display("token-1", h)
	if err != nil {
		t.Fatalf("display after legit repoint: %v", err)
	}
	if md.Image != original.Image {
		t.Fatalf("content changed after repoint: %+v", md)
	}
	if got := svc.History("token-1")[0].Commitment; got != digest {
		t.Fatalf("commitment changed after repoint")
	}
}

// 摘要不符：任何与链上承诺不一致的字节都被拒绝。
func TestDigestMismatchRefused(t *testing.T) {
	svc, h, digest := setup(t)
	h.put("https://hosting.example/meta/1.json", append(mustRaw(t, original), ' '))
	_, err := svc.Display("token-1", h)
	if !errors.Is(err, metapin.ErrDigestMismatch) {
		t.Fatalf("want ErrDigestMismatch, got %v", err)
	}
	// 承诺本身从未被改写。
	if got, ok := svcHistoryCommitment(svc, "token-1"); !ok || got != digest {
		t.Fatalf("commitment drifted: %v %v", got, ok)
	}
}

// 转移与版税：记录绑定同一承诺，转移前后藏品内容不变。
func TestTransferAndRoyaltyBindSameCommitment(t *testing.T) {
	svc, h, mintDigest := setup(t)

	if err := svc.Transfer("token-1", "alice", "bob", h); err != nil {
		t.Fatalf("transfer: %v", err)
	}
	if err := svc.RecordRoyalty("token-1", "bob", "alice", 500); err != nil {
		t.Fatalf("royalty: %v", err)
	}

	history := svc.History("token-1")
	if len(history) != 3 {
		t.Fatalf("want 3 events, got %d", len(history))
	}
	for _, ev := range history {
		if ev.Commitment != mintDigest {
			t.Fatalf("event %s bound to different commitment", ev.Kind)
		}
	}

	// 转移后展示的内容与铸造时完全一致。
	md, err := svc.Display("token-1", h)
	if err != nil {
		t.Fatalf("display after transfer: %v", err)
	}
	if md.Image != original.Image || md.Attributes[0].Value != "legendary" {
		t.Fatalf("content changed after transfer: %+v", md)
	}

	// 内容被篡改后无法按“完好”状态转移。
	h.put("https://hosting.example/meta/1.json", mustRaw(t, defaced))
	if err := svc.Transfer("token-1", "bob", "carol", h); !errors.Is(err, metapin.ErrDigestMismatch) {
		t.Fatalf("want ErrDigestMismatch on transfer of tampered token, got %v", err)
	}
}

func svcHistoryCommitment(svc *metapin.Service, tokenID string) (metapin.Digest, bool) {
	history := svc.History(tokenID)
	if len(history) == 0 {
		return metapin.Digest{}, false
	}
	return history[0].Commitment, true
}
