package connauthz

import (
	"context"
	"crypto/x509"
	"errors"
	"testing"
)

func baseTwoPathPolicy(t *testing.T, p *pki, generation uint64, revoke ...*x509.Certificate) *Policy {
	t.Helper()
	b := NewPolicyBuilder(generation).
		AddRoot(p.rootA).
		AddRoot(p.rootB).
		// 对端链只带 inter；交叉证书由服务端策略侧提供。
		AddIntermediate(p.crossAbyB)
	for _, c := range revoke {
		b.Revoke(c)
	}
	policy, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

func TestCrossSignedRevocationIsolation(t *testing.T) {
	pki := buildPKI(t)

	// 初始策略同时信任 rootA、rootB，且持有 rootA 的交叉证书 crossAbyB。
	authz, err := NewAuthorizer(baseTwoPathPolicy(t, pki, 1))
	if err != nil {
		t.Fatal(err)
	}

	// 对端链只携带 leaf + 共享 inter；两条路径完全由复验端自行构建。
	recorded := [][]byte{pki.leaf.Raw, pki.inter.Raw}

	t.Run("two verified chains share the same intermediate", func(t *testing.T) {
		d, err := authz.Authorize(context.Background(), recorded)
		if err != nil {
			t.Fatal(err)
		}
		if err := d.Err(); err != nil {
			t.Fatalf("initial authorization: %v", err)
		}
		if len(d.VerifiedChains) != 2 {
			t.Fatalf("verified chains = %d, want 2", len(d.VerifiedChains))
		}
		roots := map[string]int{}
		for _, chain := range d.VerifiedChains {
			root := chain[len(chain)-1]
			roots[root.Subject.CommonName]++
			// 两条路径必须共享同一张中间证书（同一序列号与同一 DER）。
			inter := chain[1]
			if inter.SerialNumber.Cmp(pki.inter.SerialNumber) != 0 {
				t.Fatalf("intermediate serial mismatch: %s", inter.SerialNumber)
			}
		}
		if roots["Root A"] != 1 || roots["Root B"] != 1 {
			t.Fatalf("chain roots = %v, want one Root A and one Root B", roots)
		}
	})

	t.Run("revoking crossAbyB only removes the rootB path", func(t *testing.T) {
		policy := baseTwoPathPolicy(t, pki, 2, pki.crossAbyB)
		if err := authz.Rotate(policy); err != nil {
			t.Fatal(err)
		}
		d, _ := authz.Authorize(context.Background(), recorded)
		if err := d.Err(); err != nil {
			t.Fatalf("rootA path must remain trusted: %v", err)
		}
		if len(d.VerifiedChains) != 2 {
			t.Fatalf("x509 still builds %d raw chains", len(d.VerifiedChains))
		}
		root := d.ChosenChain[len(d.ChosenChain)-1]
		if root.Subject.CommonName != "Root A" {
			t.Fatalf("chosen path root = %s, want Root A", root.Subject.CommonName)
		}
		if d.Generation != 2 {
			t.Fatalf("decision generation = %d, want 2", d.Generation)
		}
	})

	t.Run("dropping rootA trust fails only when no clean path remains", func(t *testing.T) {
		// 只信任 rootB、保留 crossAbyB：rootA 路径消失，rootB 路径仍可用。
		policy3, err := NewPolicyBuilder(3).
			AddRoot(pki.rootB).
			AddIntermediate(pki.crossAbyB).
			Build()
		if err != nil {
			t.Fatal(err)
		}
		if err := authz.Rotate(policy3); err != nil {
			t.Fatal(err)
		}
		d, _ := authz.Authorize(context.Background(), recorded)
		if err := d.Err(); err != nil {
			t.Fatalf("rootB cross-signed path must remain trusted: %v", err)
		}
		root := d.ChosenChain[len(d.ChosenChain)-1]
		if root.Subject.CommonName != "Root B" {
			t.Fatalf("chosen root = %s, want Root B", root.Subject.CommonName)
		}

		// 再撤销 crossAbyB：最后一条路径断裂，必须拒绝。
		policy4, err := NewPolicyBuilder(4).
			AddRoot(pki.rootB).
			AddIntermediate(pki.crossAbyB).
			Revoke(pki.crossAbyB).
			Build()
		if err != nil {
			t.Fatal(err)
		}
		if err := authz.Rotate(policy4); err != nil {
			t.Fatal(err)
		}
		d, _ = authz.Authorize(context.Background(), recorded)
		if err := d.Err(); !errors.Is(err, ErrRevoked) {
			t.Fatalf("want ErrRevoked when all paths are revoked, got %v", err)
		}
	})

	t.Run("revoking shared intermediate denies both paths", func(t *testing.T) {
		policy, err := NewPolicyBuilder(5).
			AddRoot(pki.rootA).
			AddRoot(pki.rootB).
			AddIntermediate(pki.crossAbyB).
			Revoke(pki.inter).
			Build()
		if err != nil {
			t.Fatal(err)
		}
		if err := authz.Rotate(policy); err != nil {
			t.Fatal(err)
		}
		d, _ := authz.Authorize(context.Background(), recorded)
		if err := d.Err(); !errors.Is(err, ErrRevoked) {
			t.Fatalf("shared intermediate revocation must deny both paths, got %v", err)
		}
	})

	t.Run("revoking leaf denies regardless of path", func(t *testing.T) {
		policy, err := NewPolicyBuilder(6).
			AddRoot(pki.rootA).
			AddRoot(pki.rootB).
			AddIntermediate(pki.crossAbyB).
			Revoke(pki.leaf).
			Build()
		if err != nil {
			t.Fatal(err)
		}
		if err := authz.Rotate(policy); err != nil {
			t.Fatal(err)
		}
		d, _ := authz.Authorize(context.Background(), recorded)
		if err := d.Err(); !errors.Is(err, ErrRevoked) {
			t.Fatalf("leaf revocation must deny, got %v", err)
		}
	})

	t.Run("self-signed root revocation is certificate scoped", func(t *testing.T) {
		// 撤销 rootA 自签名证书，不影响交叉证书 crossAbyB（同主体同密钥、不同证书），
		// rootB 路径 leaf -> inter -> crossAbyB -> rootB 仍然干净。
		policy, err := NewPolicyBuilder(7).
			AddRoot(pki.rootA).
			AddRoot(pki.rootB).
			AddIntermediate(pki.crossAbyB).
			Revoke(pki.rootA).
			Build()
		if err != nil {
			t.Fatal(err)
		}
		if err := authz.Rotate(policy); err != nil {
			t.Fatal(err)
		}
		d, _ := authz.Authorize(context.Background(), recorded)
		if err := d.Err(); err != nil {
			t.Fatalf("rootA cert revocation must not break rootB path: %v", err)
		}
		root := d.ChosenChain[len(d.ChosenChain)-1]
		if root.Subject.CommonName != "Root B" {
			t.Fatalf("chosen root = %s, want Root B", root.Subject.CommonName)
		}

		// 反向验证：撤销 rootB 自签名证书时，rootA 路径继续可用。
		policy2, err := NewPolicyBuilder(8).
			AddRoot(pki.rootA).
			AddRoot(pki.rootB).
			AddIntermediate(pki.crossAbyB).
			Revoke(pki.rootB).
			Build()
		if err != nil {
			t.Fatal(err)
		}
		if err := authz.Rotate(policy2); err != nil {
			t.Fatal(err)
		}
		d, _ = authz.Authorize(context.Background(), recorded)
		if err := d.Err(); err != nil {
			t.Fatalf("rootB cert revocation must not break rootA path: %v", err)
		}
		root = d.ChosenChain[len(d.ChosenChain)-1]
		if root.Subject.CommonName != "Root A" {
			t.Fatalf("chosen root = %s, want Root A", root.Subject.CommonName)
		}
	})
}

func TestCrossCertPresentedByPeerAlsoBuildsTwoPaths(t *testing.T) {
	pki := buildPKI(t)
	// 策略不预置交叉证书，由对端链携带：leaf, inter, crossAbyB。
	policy, err := NewPolicyBuilder(1).
		AddRoot(pki.rootA).
		AddRoot(pki.rootB).
		Build()
	if err != nil {
		t.Fatal(err)
	}
	authz, err := NewAuthorizer(policy)
	if err != nil {
		t.Fatal(err)
	}
	recorded := [][]byte{pki.leaf.Raw, pki.inter.Raw, pki.crossAbyB.Raw}
	d, err := authz.Authorize(context.Background(), recorded)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Err(); err != nil {
		t.Fatalf("authorization with peer-presented cross cert: %v", err)
	}
	if len(d.VerifiedChains) != 2 {
		t.Fatalf("verified chains = %d, want 2", len(d.VerifiedChains))
	}
}
