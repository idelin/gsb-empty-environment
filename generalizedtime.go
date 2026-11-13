package netsec

import (
	"crypto/x509"
	"encoding/asn1"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"time"
)

// ErrInvalidGeneralizedTime 表示 GeneralizedTime 不是证书要求的严格形式：
// 无小数秒的 UTC 时间并以 Z 结尾（YYYYMMDDHHMMSSZ）。
var ErrInvalidGeneralizedTime = errors.New("netsec: GeneralizedTime 必须是无小数秒的 UTC 时间并以 Z 结尾")

// strictPattern 恰好匹配 YYYYMMDDHHMMSSZ：整秒、UTC、Z 结尾。
var strictPattern = regexp.MustCompile(`^\d{14}Z$`)

// ParseGeneralizedTime 严格解析证书中的 GeneralizedTime 字符串。
// 拒绝小数秒（如 .5）、数字时区偏移（如 +0800）以及缺少 Z 的形式。
// 返回值位于 UTC，可直接用 time.Time 的 Before/After/Equal 比较。
func ParseGeneralizedTime(s string) (time.Time, error) {
	if !strictPattern.MatchString(s) {
		return time.Time{}, fmt.Errorf("%w: %q", ErrInvalidGeneralizedTime, s)
	}
	t, err := time.Parse("20060102150405", s[:len(s)-1])
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: %v", ErrInvalidGeneralizedTime, err)
	}
	return t, nil
}

// DecodeGeneralizedTime 解码 DER 编码的 ASN.1 GeneralizedTime，
// 并对证书路径强制要求严格形式。这是供证书验证代码调用的可导入裁决器。
func DecodeGeneralizedTime(der []byte) (time.Time, error) {
	var raw asn1.RawValue
	rest, err := asn1.Unmarshal(der, &raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("netsec: 解码 GeneralizedTime 失败: %w", err)
	}
	if len(rest) != 0 {
		return time.Time{}, errors.New("netsec: GeneralizedTime 后存在多余数据")
	}
	if raw.Class != asn1.ClassUniversal || raw.Tag != asn1.TagGeneralizedTime {
		return time.Time{}, fmt.Errorf("netsec: 期望 GeneralizedTime，实际 class=%d tag=%d", raw.Class, raw.Tag)
	}
	return ParseGeneralizedTime(string(raw.Bytes))
}

// tbsCertificate 仅保留定位 Validity 所需的字段。
type tbsCertificate struct {
	Raw                asn1.RawContent
	Version            int `asn1:"optional,explicit,tag:0,default:0"`
	SerialNumber       *big.Int
	SignatureAlgorithm asn1.RawValue
	Issuer             asn1.RawValue
	Validity           struct {
		NotBefore asn1.RawValue
		NotAfter  asn1.RawValue
	}
	Subject    asn1.RawValue
	PublicKey  asn1.RawValue
	Extensions []asn1.RawValue `asn1:"optional,explicit,tag:3"`
}

// CheckCertificateValidityTimes 从证书的原始 TBSCertificate 重新解码
// notBefore/notAfter，凡使用 GeneralizedTime 者必须为严格形式，
// 否则判定证书无效。UTCTime 按定义总是 Z 结尾，直接放行。
func CheckCertificateValidityTimes(cert *x509.Certificate) error {
	var tbs tbsCertificate
	rest, err := asn1.Unmarshal(cert.RawTBSCertificate, &tbs)
	if err != nil {
		return fmt.Errorf("netsec: 解析 TBSCertificate 失败: %w", err)
	}
	if len(rest) != 0 {
		return errors.New("netsec: TBSCertificate 后存在多余数据")
	}
	for _, field := range []struct {
		name string
		raw  asn1.RawValue
	}{
		{"notBefore", tbs.Validity.NotBefore},
		{"notAfter", tbs.Validity.NotAfter},
	} {
		if field.raw.Tag != asn1.TagGeneralizedTime {
			continue
		}
		if _, err := ParseGeneralizedTime(string(field.raw.Bytes)); err != nil {
			return fmt.Errorf("netsec: 证书 %s 无效: %w", field.name, err)
		}
	}
	return nil
}
