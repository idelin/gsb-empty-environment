package admission_test

import (
	"fmt"
	"time"

	"example.org/netsec/q0762/admission"
)

// ExampleAdmit 展示一次完整的准入过滤调用：白名单、预算与验证回调均由
// 调用方提供，过滤器负责结构裁剪、去重与墓碑维护。
func ExampleAdmit() {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	const fp = "PRIMARY-KEY-FP"

	pk := &admission.PublicKeyPacket{
		Fingerprint: fp,
		PrimaryRaw:  []byte("primary"),
		UserIDs: []admission.UserID{{
			Raw:     []byte("uid-alice"),
			Created: base,
			Signatures: []admission.Signature{
				{Raw: []byte("self-1"), Issuer: fp, Created: base, Self: true},
				{Raw: []byte("good-1"), Issuer: "trusted-ca", Created: base},
				{Raw: []byte("junk-1"), Issuer: "random-stranger", Created: base},
			},
		}},
	}

	verifier := func(_ *admission.PublicKeyPacket, _ admission.UserID, sig admission.Signature) bool {
		return true // 真实场景在此执行 OpenPGP 密码学验证
	}

	res, err := admission.Admit(pk, admission.Options{
		Whitelist: admission.NewWhitelist("trusted-ca"),
		Budget:    1000,
		Verify:    verifier,
	})
	if err != nil {
		fmt.Println("rejected:", err)
		return
	}

	uid := res.Packet.UserIDs[0]
	fmt.Println("uids kept:", res.Stats.UIDsKept)
	fmt.Println("signatures kept:", len(uid.Signatures))
	fmt.Println("tombstones:", len(res.Tombstones))
	// Output:
	// uids kept: 1
	// signatures kept: 2
	// tombstones: 1
}

func ExampleStore_Merge() {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	const fp = "PRIMARY-KEY-FP"
	verifier := func(_ *admission.PublicKeyPacket, _ admission.UserID, _ admission.Signature) bool {
		return true
	}
	opts := admission.Options{
		Whitelist: admission.NewWhitelist("trusted-ca"),
		Budget:    -1,
		Verify:    verifier,
	}

	store := admission.NewStore()
	junk := admission.Signature{Raw: []byte("junk"), Issuer: "stranger", Created: base}
	first := &admission.PublicKeyPacket{
		Fingerprint: fp,
		PrimaryRaw:  []byte("primary"),
		UserIDs: []admission.UserID{{
			Raw:     []byte("uid-alice"),
			Created: base,
			Signatures: []admission.Signature{
				{Raw: []byte("self"), Issuer: fp, Created: base, Self: true},
				junk,
			},
		}},
	}
	if _, err := store.Merge(first, opts); err != nil {
		fmt.Println("merge error:", err)
		return
	}

	// 重放此前被丢弃的签名：墓碑机制阻止它复活。
	replay := &admission.PublicKeyPacket{
		Fingerprint: fp,
		PrimaryRaw:  []byte("primary"),
		UserIDs: []admission.UserID{{
			Raw:        []byte("uid-alice"),
			Created:    base,
			Signatures: []admission.Signature{junk},
		}},
	}
	res, err := store.Merge(replay, opts)
	if err != nil {
		fmt.Println("merge error:", err)
		return
	}
	fmt.Println("third-party after replay:", countTP(res.Packet))
	// Output:
	// third-party after replay: 0
}

func countTP(pk *admission.PublicKeyPacket) int {
	n := 0
	for _, uid := range pk.UserIDs {
		for _, sig := range uid.Signatures {
			if !sig.Self {
				n++
			}
		}
	}
	return n
}
