package keyusage

import "fmt"

// Key 描述一把受管密钥的声明信息。
//
// 字段说明：
//   - ID：密钥标识；
//   - Algorithm：密钥绑定的算法，必须在白名单内；
//   - Usages：密钥声明的用途集合；
//   - ParentID：非空时表示该密钥是由父密钥派生而来。
type Key struct {
	ID        string
	Algorithm Algorithm
	Usages    []Usage
	ParentID  string
}

// IsDerived 报告密钥是否为派生密钥。
func (k Key) IsDerived() bool { return k.ParentID != "" }

// Declares 报告密钥是否声明了指定用途。
func (k Key) Declares(u Usage) bool {
	for _, declared := range k.Usages {
		if declared == u {
			return true
		}
	}
	return false
}

// hasUsageFamilyConflict 检查同一密钥是否同时声明了签名族与保密族用途。
// 签名密钥不得用于加解密，反之亦然，创建阶段即拒绝这种声明。
func (k Key) hasUsageFamilyConflict() bool {
	var hasSigning, hasConfidentiality bool
	for _, u := range k.Usages {
		switch {
		case IsSigningUsage(u):
			hasSigning = true
		case IsConfidentialityUsage(u):
			hasConfidentiality = true
		}
	}
	return hasSigning && hasConfidentiality
}

// Validate 在密钥登记/派生时校验声明本身是否合法：
// 用途与算法必须已知、算法必须在白名单内、用途必须受算法支持，
// 且不得跨越签名/保密用途族。
func (k Key) Validate() error {
	if k.ID == "" {
		return fmt.Errorf("keyusage: key id is empty")
	}
	if len(k.Usages) == 0 {
		return fmt.Errorf("keyusage: key %q declares no usages", k.ID)
	}

	seen := make(map[Usage]bool, len(k.Usages))
	for _, u := range k.Usages {
		if !IsKnownUsage(u) {
			return fmt.Errorf("keyusage: key %q declares unknown usage %q", k.ID, u)
		}
		if seen[u] {
			return fmt.Errorf("keyusage: key %q declares duplicate usage %q", k.ID, u)
		}
		seen[u] = true
	}

	if IsWeak(k.Algorithm) {
		return fmt.Errorf("keyusage: key %q uses weak algorithm %q", k.ID, k.Algorithm)
	}
	if !IsWhitelisted(k.Algorithm) {
		return fmt.Errorf("keyusage: key %q uses non-whitelisted algorithm %q", k.ID, k.Algorithm)
	}

	for _, u := range k.Usages {
		if !algorithmAllows(k.Algorithm, u) {
			return fmt.Errorf("keyusage: algorithm %q does not support usage %q on key %q",
				k.Algorithm, u, k.ID)
		}
	}

	if k.hasUsageFamilyConflict() {
		return fmt.Errorf("keyusage: key %q mixes signing and confidentiality usages", k.ID)
	}
	return nil
}
