package netsec

import (
	"errors"
	"testing"
	"time"
)

func validityWindow() (after, before uint64) {
	now := time.Now()
	return uint64(now.Add(-time.Minute).Unix()), uint64(now.Add(time.Hour).Unix())
}

// 任务场景：另一台主机的主机CA也被登记成了用户CA，
// 用该主机CA的密钥签发一张用户证书，验证方予以接受。
func TestHostCASignedUserCertificateAccepted(t *testing.T) {
	hostCAPub, hostCAKey, err := GenerateKey() // 另一台主机的主机CA
	if err != nil {
		t.Fatal(err)
	}
	userPub, _, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}

	auth := NewAuthority()
	auth.RegisterHostCA(hostCAPub)
	auth.RegisterUserCA(hostCAPub) // 主机CA同时被当成用户CA登记

	after, before := validityWindow()
	cert, err := SignUserCert(hostCAKey, userPub, "remote-host-ops", []string{"ops", "deploy"}, 1, after, before)
	if err != nil {
		t.Fatal(err)
	}

	// 序列化后再解析，模拟证书经网络传到 SSH 服务。
	parsed, err := ParseCertificate(cert.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Type != CertTypeUser {
		t.Fatalf("cert type = %d, want %d", parsed.Type, CertTypeUser)
	}
	if parsed.KeyID != "remote-host-ops" {
		t.Fatalf("key id = %q", parsed.KeyID)
	}

	if err := auth.VerifyUserCertificate(parsed, "ops"); err != nil {
		t.Fatalf("host-CA-signed user certificate rejected: %v", err)
	}
}

// 主机CA密钥未登记为用户CA时，签发的用户证书必须被拒绝：
// 主机CA与用户CA的密钥不能互换。
func TestHostCAKeyNotInterchangeable(t *testing.T) {
	hostCAPub, hostCAKey, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	userPub, _, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}

	auth := NewAuthority()
	auth.RegisterHostCA(hostCAPub) // 仅登记为主机CA

	after, before := validityWindow()
	cert, err := SignUserCert(hostCAKey, userPub, "forged", []string{"ops"}, 2, after, before)
	if err != nil {
		t.Fatal(err)
	}

	err = auth.VerifyUserCertificate(cert, "ops")
	if !errors.Is(err, ErrHostCAKeyNotUserCA) {
		t.Fatalf("err = %v, want %v", err, ErrHostCAKeyNotUserCA)
	}
}

// 正常路径：用户CA签发且签名正确的证书被接受。
func TestUserCASignedCertificateAccepted(t *testing.T) {
	userCAPub, userCAKey, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	userPub, _, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}

	auth := NewAuthority()
	auth.RegisterUserCA(userCAPub)

	after, before := validityWindow()
	cert, err := SignUserCert(userCAKey, userPub, "alice", []string{"alice"}, 3, after, before)
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.VerifyUserCertificate(cert, "alice"); err != nil {
		t.Fatalf("user-CA-signed certificate rejected: %v", err)
	}
	if err := auth.VerifyUserCertificate(cert, "bob"); !errors.Is(err, ErrPrincipal) {
		t.Fatalf("err = %v, want %v", err, ErrPrincipal)
	}
}

// 篡改后的证书必须因签名错误被拒绝。
func TestTamperedCertificateRejected(t *testing.T) {
	userCAPub, userCAKey, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	userPub, _, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}

	auth := NewAuthority()
	auth.RegisterUserCA(userCAPub)

	after, before := validityWindow()
	cert, err := SignUserCert(userCAKey, userPub, "alice", []string{"alice"}, 4, after, before)
	if err != nil {
		t.Fatal(err)
	}

	blob := cert.Bytes()
	// 篡改 nonce（紧跟算法名之后的字段），保持结构可解析但签名失效。
	blob[4+len(UserCertAlgorithm)+4] ^= 0xff
	parsed, err := ParseCertificate(blob)
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.VerifyUserCertificate(parsed, "alice"); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("err = %v, want %v", err, ErrBadSignature)
	}
}

// 过期的证书必须被拒绝。
func TestExpiredCertificateRejected(t *testing.T) {
	userCAPub, userCAKey, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	userPub, _, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}

	auth := NewAuthority()
	auth.RegisterUserCA(userCAPub)

	now := time.Now()
	cert, err := SignUserCert(userCAKey, userPub, "alice", []string{"alice"}, 5,
		uint64(now.Add(-2*time.Hour).Unix()), uint64(now.Add(-time.Hour).Unix()))
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.VerifyUserCertificate(cert, "alice"); !errors.Is(err, ErrExpired) {
		t.Fatalf("err = %v, want %v", err, ErrExpired)
	}
}
