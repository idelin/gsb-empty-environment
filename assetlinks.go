package netsec

import (
	"encoding/json"
	"fmt"
	"strings"
)

// RelationHandleAllURLs is the only relation that authorizes an Android app
// to handle all URLs of a domain (Android App Links).
const RelationHandleAllURLs = "delegate_permission/common.handle_all_urls"

// NamespaceAndroidApp is the target namespace identifying an Android app.
const NamespaceAndroidApp = "android_app"

// sha256CertBytes is the byte length of a SHA-256 certificate fingerprint.
const sha256CertBytes = 32

// Statement is a single entry of an assetlinks.json document.
type Statement struct {
	Relation []string `json:"relation"`
	Target   Target   `json:"target"`
}

// Target is the subject a Statement applies to.
type Target struct {
	Namespace              string   `json:"namespace"`
	PackageName            string   `json:"package_name"`
	SHA256CertFingerprints []string `json:"sha256_cert_fingerprints"`
}

// ParseStatements decodes an assetlinks.json document.
func ParseStatements(data []byte) ([]Statement, error) {
	var stmts []Statement
	if err := json.Unmarshal(data, &stmts); err != nil {
		return nil, fmt.Errorf("assetlinks: invalid JSON: %w", err)
	}
	return stmts, nil
}

// NormalizeFingerprint validates a colon-separated hexadecimal SHA-256
// fingerprint and returns its canonical uppercase form. The check is
// case-insensitive: "aa:bb:..." and "AA:BB:..." normalize identically.
func NormalizeFingerprint(fp string) (string, error) {
	parts := strings.Split(fp, ":")
	if len(parts) != sha256CertBytes {
		return "", fmt.Errorf("assetlinks: fingerprint %q: want %d octets, got %d", fp, sha256CertBytes, len(parts))
	}
	for _, p := range parts {
		if len(p) != 2 || !isHex(p[0]) || !isHex(p[1]) {
			return "", fmt.Errorf("assetlinks: fingerprint %q: malformed octet %q", fp, p)
		}
	}
	return strings.ToUpper(fp), nil
}

func isHex(c byte) bool {
	return ('0' <= c && c <= '9') || ('a' <= c && c <= 'f') || ('A' <= c && c <= 'F')
}

// Qualifies reports whether the statement grants Android App Links handling:
// its relations must include delegate_permission/common.handle_all_urls and
// its target namespace must be android_app.
func (s Statement) Qualifies() bool {
	if s.Target.Namespace != NamespaceAndroidApp {
		return false
	}
	for _, rel := range s.Relation {
		if rel == RelationHandleAllURLs {
			return true
		}
	}
	return false
}

// AuthorizedFingerprints returns the union of the normalized SHA-256
// certificate fingerprints that qualifying statements declare for
// packageName. Malformed fingerprint entries never match and are skipped.
func AuthorizedFingerprints(stmts []Statement, packageName string) map[string]struct{} {
	out := make(map[string]struct{})
	for _, s := range stmts {
		if !s.Qualifies() || s.Target.PackageName != packageName {
			continue
		}
		for _, fp := range s.Target.SHA256CertFingerprints {
			if norm, err := NormalizeFingerprint(fp); err == nil {
				out[norm] = struct{}{}
			}
		}
	}
	return out
}

// VerifyPackage reports whether any of the app's signing certificate
// fingerprints is exactly equal (after case-insensitive normalization) to a
// fingerprint authorized for packageName by the given statements.
func VerifyPackage(stmts []Statement, packageName string, appCertFingerprints []string) bool {
	authorized := AuthorizedFingerprints(stmts, packageName)
	for _, fp := range appCertFingerprints {
		norm, err := NormalizeFingerprint(fp)
		if err != nil {
			continue
		}
		if _, ok := authorized[norm]; ok {
			return true
		}
	}
	return false
}
