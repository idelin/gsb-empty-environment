package connauthz

import (
	"context"
	"errors"
	"testing"
)

// TestTLSConnection_ReauthorizedOnEveryNewStream 用真实 TLS 握手验证：
// 握手时记录的链在后续代次下被重新校验，长连接不持有“永久授权”。
func TestTLSConnection_ReauthorizedOnEveryNewStream(t *testing.T) {
	pki := buildPKI(t)
	serverCfg := pki.serverConfig(pki.poolOf(pki.rootA, pki.rootB))
	// 对端链携带 leaf + inter；交叉证书由服务端策略提供。
	clientCfg := pki.clientConfig(pki.inter)

	serverConn, _ := handshakeOverPipe(t, serverCfg, clientCfg)

	policy1, err := NewPolicyBuilder(1).
		AddRoot(pki.rootA).
		AddRoot(pki.rootB).
		AddIntermediate(pki.crossAbyB).
		Build()
	if err != nil {
		t.Fatal(err)
	}
	authz, err := NewAuthorizer(policy1)
	if err != nil {
		t.Fatal(err)
	}

	// 握手阶段只记录证书链。
	state := serverConn.ConnectionState()
	if len(state.PeerCertificates) != 2 {
		t.Fatalf("peer certificates = %d, want leaf+inter", len(state.PeerCertificates))
	}
	conn := ConnectionFromTLS(state)
	if len(conn.RawChain()) != 2 {
		t.Fatalf("recorded chain length = %d, want 2", len(conn.RawChain()))
	}

	// 第一代：复验通过。
	d, err := conn.Authorize(context.Background(), authz)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Err(); err != nil {
		t.Fatalf("gen1 authorization: %v", err)
	}

	// 第二代：连接保持打开，但叶子被撤销，新流复验必须被拒。
	policy2, err := NewPolicyBuilder(2).
		AddRoot(pki.rootA).
		AddRoot(pki.rootB).
		AddIntermediate(pki.crossAbyB).
		Revoke(pki.leaf).
		Build()
	if err != nil {
		t.Fatal(err)
	}
	if err := authz.Rotate(policy2); err != nil {
		t.Fatal(err)
	}
	d, err = conn.Authorize(context.Background(), authz)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Err(); !errors.Is(err, ErrRevoked) || d.Generation != 2 {
		t.Fatalf("gen2 want ErrRevoked, got %v (gen %d)", err, d.Generation)
	}

	// 连接本身未被授权层关闭，底层 TLS 仍可查询状态。
	if err := serverConn.HandshakeContext(context.Background()); err != nil {
		t.Fatalf("long-lived connection must stay open: %v", err)
	}

	// 第三代：换根（只保留 rootB 与交叉证书）后路径仍可构建并放行。
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
	d, _ = conn.Authorize(context.Background(), authz)
	if err := d.Err(); err != nil {
		t.Fatalf("gen3 rootB path should pass: %v", err)
	}
	if root := d.ChosenChain[len(d.ChosenChain)-1]; root.Subject.CommonName != "Root B" {
		t.Fatalf("gen3 root = %s, want Root B", root.Subject.CommonName)
	}
}

func TestTLSConnection_EmptyAndExpiredChain(t *testing.T) {
	pki := buildPKI(t)

	t.Run("no client certificate", func(t *testing.T) {
		// 空证书链可能来自未请求客户端证书的监听器或其他传输封装，
		// 授权层必须显式拒绝而不是放行。
		conn := &Connection{}
		policy, _ := NewPolicyBuilder(1).AddRoot(pki.rootA).Build()
		authz, err := NewAuthorizer(policy)
		if err != nil {
			t.Fatal(err)
		}
		d, _ := conn.Authorize(context.Background(), authz)
		if err := d.Err(); !errors.Is(err, ErrNoClientCertificate) {
			t.Fatalf("want ErrNoClientCertificate, got %v", err)
		}
	})

	t.Run("expired leaf rejected by fresh verify", func(t *testing.T) {
		policy, _ := NewPolicyBuilder(1).
			AddRoot(pki.rootA).
			AddRoot(pki.rootB).
			AddIntermediate(pki.crossAbyB).
			Build()
		authz, err := NewAuthorizer(policy)
		if err != nil {
			t.Fatal(err)
		}
		recorded := [][]byte{pki.expiredLeaf.Raw, pki.inter.Raw}
		d, _ := authz.Authorize(context.Background(), recorded)
		if err := d.Err(); !errors.Is(err, ErrUntrustedChain) {
			t.Fatalf("expired leaf want ErrUntrustedChain, got %v", err)
		}
	})

	t.Run("malformed DER rejected", func(t *testing.T) {
		policy, _ := NewPolicyBuilder(1).AddRoot(pki.rootA).Build()
		authz, _ := NewAuthorizer(policy)
		d, _ := authz.Authorize(context.Background(), [][]byte{{0x01, 0x02, 0x03}})
		if err := d.Err(); !errors.Is(err, ErrMalformedChain) {
			t.Fatalf("want ErrMalformedChain, got %v", err)
		}
	})
}
