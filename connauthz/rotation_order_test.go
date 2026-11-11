package connauthz

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"sync"
	"sync/atomic"
	"testing"
)

// tlsStateWith 构造一个携带给定 DER 证书的 TLS 连接状态，用于测试记录拷贝。
func tlsStateWith(der ...[]byte) tls.ConnectionState {
	certs := make([]*x509.Certificate, len(der))
	for i, raw := range der {
		cert, err := x509.ParseCertificate(raw)
		if err != nil {
			panic(err)
		}
		certs[i] = cert
	}
	return tls.ConnectionState{PeerCertificates: certs}
}

// TestRotationAndOpenStreamOrdering 在 -race 下验证轮换与开流并发时的顺序保证：
//   - 每次判定的 Generation 与当时生效策略的判定结果一致；
//   - 单个工作协程观察到的代次单调不减（代次永不回退）；
//   - Rotate 全部完成后，所有新判定都落在最后一代。
func TestRotationAndOpenStreamOrdering(t *testing.T) {
	pki := buildPKI(t)

	const totalGens = 12
	policies := make([]*Policy, totalGens)
	for g := 1; g <= totalGens; g++ {
		b := NewPolicyBuilder(uint64(g)).
			AddRoot(pki.rootA).
			AddRoot(pki.rootB).
			AddIntermediate(pki.crossAbyB)
		// 交替撤销/恢复叶子：撤销代拒绝、非撤销代放行。
		if g%2 == 0 {
			b.Revoke(pki.leaf)
		}
		policy, err := b.Build()
		if err != nil {
			t.Fatal(err)
		}
		policies[g-1] = policy
	}

	authz, err := NewAuthorizer(policies[0])
	if err != nil {
		t.Fatal(err)
	}
	recorded := [][]byte{pki.leaf.Raw, pki.inter.Raw}

	const workers = 8
	var wg sync.WaitGroup
	var stop atomic.Bool

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lastGen := uint64(0)
			for !stop.Load() {
				d, err := authz.Authorize(context.Background(), recorded)
				if err != nil {
					t.Errorf("authorize: %v", err)
					return
				}
				gen := d.Generation
				if gen < lastGen {
					t.Errorf("generation went backwards: %d -> %d", lastGen, gen)
					return
				}
				lastGen = gen

				// 代次与策略快照必须自洽：偶数代撤销叶子 => 拒绝。
				wantAllowed := gen%2 == 1
				if d.Allowed != wantAllowed {
					t.Errorf("gen %d: allowed=%v want %v", gen, d.Allowed, wantAllowed)
					return
				}
				if err := d.Err(); (err == nil) != wantAllowed {
					t.Errorf("gen %d: decision.Err mismatch: %v", gen, err)
					return
				}
			}
		}()
	}

	for g := 2; g <= totalGens; g++ {
		if err := authz.Rotate(policies[g-1]); err != nil {
			t.Fatalf("rotate gen %d: %v", g, err)
		}
		if authz.Current().Generation() != uint64(g) {
			t.Fatalf("current gen = %d, want %d", authz.Current().Generation(), g)
		}
	}

	// Rotate 返回后新代全局生效：静止状态下的判定必为最后一代。
	stop.Store(true)
	wg.Wait()

	d, err := authz.Authorize(context.Background(), recorded)
	if err != nil {
		t.Fatal(err)
	}
	if d.Generation != totalGens {
		t.Fatalf("post-rotation generation = %d, want %d", d.Generation, totalGens)
	}
	if d.Allowed {
		t.Fatalf("final gen %d revokes leaf, must deny", totalGens)
	}
}

func TestPolicyImmutabilityAndBuilderReuse(t *testing.T) {
	pki := buildPKI(t)
	builder := NewPolicyBuilder(1).AddRoot(pki.rootA)
	policy, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}

	// Build 后继续修改 builder，不影响已生成策略。
	builder.Revoke(pki.leaf)
	if policy.IsRevoked(pki.leaf) {
		t.Fatal("built policy must be immutable against later builder mutations")
	}

	// 记录链的防御性拷贝：外部修改原始切片不影响 Connection。
	conn := ConnectionFromTLS(tlsStateWith(pki.leaf.Raw, pki.inter.Raw))
	raw := conn.RawChain()
	raw[0][0] ^= 0xFF
	again := conn.RawChain()
	if again[0][0] == raw[0][0] {
		t.Fatal("RawChain must return independent copies")
	}
}
