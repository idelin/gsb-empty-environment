package netsec_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"time"

	netsec "example.org/netsec/q0138"
)

// ExampleVerifyAccess 演示资源服务如何校验一条逐跳收窄的委托链，
// 并据此放行末端主体的资源访问请求。
func ExampleVerifyAccess() {
	rootPub, rootPriv, _ := ed25519.GenerateKey(rand.Reader)
	alicePub, alicePriv, _ := ed25519.GenerateKey(rand.Reader)
	bobPub, _, _ := ed25519.GenerateKey(rand.Reader)

	now := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)

	// 根签发者把家目录下的全部 CRUD 能力委托给 Alice，两小时后过期。
	rootToken, err := netsec.Sign(rootPriv, alicePub, now.Add(2*time.Hour),
		[]netsec.Capability{{Resource: "fs:/home/alice/*", Action: "crud/*"}}, "")
	if err != nil {
		panic(err)
	}

	// Alice 只能继续收窄：单个文件的只读权限，过期时间不晚于根凭证。
	leafToken, err := netsec.Sign(alicePriv, bobPub, now.Add(time.Hour),
		[]netsec.Capability{{Resource: "fs:/home/alice/docs/report", Action: "crud/read"}},
		rootToken.Encode())
	if err != nil {
		panic(err)
	}

	chain := netsec.Chain{Tokens: []*netsec.Token{rootToken, leafToken}}
	opts := netsec.VerifyOptions{
		TrustedIssuers: map[string]bool{netsec.DIDFromPublicKey(rootPub): true},
		Now:            now,
	}

	requested := netsec.Capability{Resource: "fs:/home/alice/docs/report", Action: "crud/read"}
	if err := netsec.VerifyAccess(chain, requested, opts); err != nil {
		fmt.Println("denied:", err)
		return
	}
	fmt.Println("access granted")
	// Output: access granted
}
