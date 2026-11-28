package pathverify

import (
	"testing"
	"time"
)

// TestSANOverridesCN 验收点 3：存在 DNS SAN 时只校验 SAN，彻底忽略 Subject CN。
func TestSANOverridesCN(t *testing.T) {
	root := createCA("Host Root", mustGenerateKey(),
		fixedNow.Add(-time.Hour), fixedNow.Add(time.Hour), 0, true)
	roots := NewCertPool()
	roots.AddCert(root.cert)

	// CN 与实际主机名相同，但 SAN 指向另一个名称：必须拒绝（CN 被忽略）。
	spec := defaultLeafSpec()
	spec.cn = "service.example.com"
	spec.dnsSAN = []string{"other.example.com"}
	leaf := issueLeaf(spec, root)

	opts := baseOptions(leaf, roots, NewCertPool())
	if err := expectReasonOpts(opts, ReasonHostname); err != nil {
		t.Fatalf("SAN must override CN: %v", err)
	}

	// 主机名命中 SAN 即通过，哪怕与 CN 不同。
	opts.DNSName = "other.example.com"
	if _, err := Verify(opts); err != nil {
		t.Fatalf("hostname matching a DNS SAN should pass, got %v", err)
	}
}

// TestCNFallbackWithoutSAN 完全没有 SAN 时回退使用 Subject CN。
func TestCNFallbackWithoutSAN(t *testing.T) {
	root := createCA("CN Root", mustGenerateKey(),
		fixedNow.Add(-time.Hour), fixedNow.Add(time.Hour), 0, true)
	roots := NewCertPool()
	roots.AddCert(root.cert)

	spec := defaultLeafSpec()
	spec.dnsSAN = nil
	spec.cn = "legacy.example.com"
	leaf := issueLeaf(spec, root)

	opts := baseOptions(leaf, roots, NewCertPool())
	opts.DNSName = "legacy.example.com"
	if _, err := Verify(opts); err != nil {
		t.Fatalf("CN fallback should match when no SAN present, got %v", err)
	}
	opts.DNSName = "service.example.com"
	if err := expectReasonOpts(opts, ReasonHostname); err != nil {
		t.Fatal(err)
	}
}

// TestWildcardHostname 最左标签通配符恰好匹配一个标签。
func TestWildcardHostname(t *testing.T) {
	root := createCA("Wild Root", mustGenerateKey(),
		fixedNow.Add(-time.Hour), fixedNow.Add(time.Hour), 0, true)
	roots := NewCertPool()
	roots.AddCert(root.cert)

	spec := defaultLeafSpec()
	spec.cn = "wild.example.com"
	spec.dnsSAN = []string{"*.example.com"}
	leaf := issueLeaf(spec, root)

	opts := baseOptions(leaf, roots, NewCertPool())
	opts.DNSName = "api.example.com"
	if _, err := Verify(opts); err != nil {
		t.Fatalf("wildcard should match single leftmost label, got %v", err)
	}
	opts.DNSName = "a.b.example.com"
	if err := expectReasonOpts(opts, ReasonHostname); err != nil {
		t.Fatalf("wildcard must not match multiple labels: %v", err)
	}
	opts.DNSName = "example.com"
	if err := expectReasonOpts(opts, ReasonHostname); err != nil {
		t.Fatalf("wildcard must require one label: %v", err)
	}
}
