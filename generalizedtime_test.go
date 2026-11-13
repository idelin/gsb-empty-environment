package netsec

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"math/big"
	"testing"
	"time"
)

func TestParseGeneralizedTimeValid(t *testing.T) {
	for _, s := range []string{
		"20261113120000Z",
		"20500101000000Z",
		"19991231235959Z",
	} {
		got, err := ParseGeneralizedTime(s)
		if err != nil {
			t.Errorf("ParseGeneralizedTime(%q) 意外失败: %v", s, err)
			continue
		}
		if got.Location() != time.UTC {
			t.Errorf("ParseGeneralizedTime(%q) 时区 = %v，期望 UTC", s, got.Location())
		}
	}
}

func TestParseGeneralizedTimeInvalid(t *testing.T) {
	for _, s := range []string{
		"20261113120000.5Z",       // 小数秒
		"20261113120000.123Z",     // 小数秒
		"20261113120000+0800",     // 本地时区偏移
		"20261113120000.5+0800",   // 小数秒 + 时区偏移
		"20261113120000-0500",     // 负时区偏移
		"20261113120000",          // 缺少 Z
		"2026111312000Z",          // 位数不足
		"202611131200000Z",        // 位数过多
		"20261313120000Z",         // 月份 13
		"20261113246000Z",         // 小时 24
		"20260230120000Z",         // 2 月 30 日
		"20261113120000z",         // 小写 z
	} {
		if _, err := ParseGeneralizedTime(s); !errors.Is(err, ErrInvalidGeneralizedTime) {
			t.Errorf("ParseGeneralizedTime(%q) 错误 = %v，期望 ErrInvalidGeneralizedTime", s, err)
		}
	}
}

func TestParseGeneralizedTimeComparable(t *testing.T) {
	a, err := ParseGeneralizedTime("20261113120000Z")
	if err != nil {
		t.Fatal(err)
	}
	b, err := ParseGeneralizedTime("20261113120001Z")
	if err != nil {
		t.Fatal(err)
	}
	if !a.Before(b) || !b.After(a) {
		t.Error("以 Z 结尾的整秒时间应可比较先后")
	}
	again, err := ParseGeneralizedTime("20261113120000Z")
	if err != nil {
		t.Fatal(err)
	}
	if !a.Equal(again) {
		t.Error("相同的 Z 结尾整秒时间应相等")
	}
	// 同一物理时刻的 +0800 写法必须被拒绝，而不是被当成另一时刻。
	if _, err := ParseGeneralizedTime("20261113200000+0800"); err == nil {
		t.Error("带 +0800 的形式应被拒绝")
	}
}

func derGeneralizedTime(s string) []byte {
	return append([]byte{byte(asn1.TagGeneralizedTime), byte(len(s))}, s...)
}

func TestDecodeGeneralizedTime(t *testing.T) {
	if _, err := DecodeGeneralizedTime(derGeneralizedTime("20500101000000Z")); err != nil {
		t.Errorf("严格形式应解码成功: %v", err)
	}
	for _, s := range []string{
		"20500101000000.5Z",   // 小数秒
		"20500101000000+0800", // 本地时区
	} {
		if _, err := DecodeGeneralizedTime(derGeneralizedTime(s)); !errors.Is(err, ErrInvalidGeneralizedTime) {
			t.Errorf("DecodeGeneralizedTime(%q) 错误 = %v，期望 ErrInvalidGeneralizedTime", s, err)
		}
	}
	// 标签不是 GeneralizedTime（此处为 UTCTime）。
	if _, err := DecodeGeneralizedTime([]byte{byte(asn1.TagUTCTime), 13, '2', '6', '1', '1', '1', '3', '1', '2', '0', '0', '0', '0', 'Z'}); err == nil {
		t.Error("非 GeneralizedTime 标签应失败")
	}
	// 多余尾随数据。
	if _, err := DecodeGeneralizedTime(append(derGeneralizedTime("20500101000000Z"), 0x05, 0x00)); err == nil {
		t.Error("尾随数据应失败")
	}
}

func TestCheckCertificateValidityTimesRealCert(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "strict-gt"},
		NotBefore:    time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),  // UTCTime
		NotAfter:     time.Date(2060, 1, 1, 0, 0, 0, 0, time.UTC),  // GeneralizedTime
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckCertificateValidityTimes(cert); err != nil {
		t.Errorf("标准库签发的证书应通过: %v", err)
	}
}

// certificateWithRawValidity 构造 RawTBSCertificate 中 Validity 为指定原始值的证书。
func certificateWithRawValidity(t *testing.T, notBefore, notAfter asn1.RawValue) *x509.Certificate {
	t.Helper()
	nb, err := asn1.Marshal(notBefore)
	if err != nil {
		t.Fatal(err)
	}
	na, err := asn1.Marshal(notAfter)
	if err != nil {
		t.Fatal(err)
	}
	validity := append(nb, na...)
	emptySeq := asn1.RawValue{Class: asn1.ClassUniversal, Tag: asn1.TagSequence, IsCompound: true}
	tbs, err := asn1.Marshal(struct {
		SerialNumber       int
		SignatureAlgorithm asn1.RawValue
		Issuer             asn1.RawValue
		Validity           asn1.RawValue
		Subject            asn1.RawValue
		PublicKey          asn1.RawValue
	}{
		SerialNumber:       1,
		SignatureAlgorithm: emptySeq,
		Issuer:             emptySeq,
		Validity: asn1.RawValue{
			Class: asn1.ClassUniversal, Tag: asn1.TagSequence, IsCompound: true, Bytes: validity,
		},
		Subject:   emptySeq,
		PublicKey: emptySeq,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &x509.Certificate{RawTBSCertificate: tbs}
}

func rawGeneralizedTime(s string) asn1.RawValue {
	return asn1.RawValue{Class: asn1.ClassUniversal, Tag: asn1.TagGeneralizedTime, Bytes: []byte(s)}
}

func TestCheckCertificateValidityTimesRejectsNonStrict(t *testing.T) {
	for _, bad := range []string{
		"20500101000000.5Z",   // 小数秒
		"20500101000000+0800", // 本地时区
	} {
		cert := certificateWithRawValidity(t, rawGeneralizedTime(bad), rawGeneralizedTime("20510101000000Z"))
		if err := CheckCertificateValidityTimes(cert); !errors.Is(err, ErrInvalidGeneralizedTime) {
			t.Errorf("notBefore=%q 的证书应被判定无效: %v", bad, err)
		}
		cert = certificateWithRawValidity(t, rawGeneralizedTime("20500101000000Z"), rawGeneralizedTime(bad))
		if err := CheckCertificateValidityTimes(cert); !errors.Is(err, ErrInvalidGeneralizedTime) {
			t.Errorf("notAfter=%q 的证书应被判定无效: %v", bad, err)
		}
	}
}

func TestCheckCertificateValidityTimesAcceptsStrict(t *testing.T) {
	cert := certificateWithRawValidity(t,
		rawGeneralizedTime("20500101000000Z"),
		rawGeneralizedTime("20510101000000Z"),
	)
	if err := CheckCertificateValidityTimes(cert); err != nil {
		t.Errorf("严格形式的证书应通过: %v", err)
	}
}
