package oci

import (
	"path"
	"regexp"
	"strings"
)

var privateKeyPattern = regexp.MustCompile(`-----BEGIN (?:[A-Z0-9 ]+ )?PRIVATE KEY-----`)
var awsKeyPattern = regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`)
var githubTokenPattern = regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9_]{20,}\b`)
var googleAPIKeyPattern = regexp.MustCompile(`\bAIza[0-9A-Za-z_\-]{35}\b`)

// DefaultSensitivePathGlobs lists credential and secret locations. Matching is
// root-relative and also supports basename matching for slash-free entries.
var DefaultSensitivePathGlobs = []string{
	"etc/shadow",
	"etc/shadow-",
	"etc/gshadow",
	"etc/gshadow-",
	"etc/security/opasswd",
	"root/.ssh/id_rsa",
	"root/.ssh/id_dsa",
	"root/.ssh/id_ecdsa",
	"root/.ssh/id_ed25519",
	"home/*/.ssh/id_rsa",
	"home/*/.ssh/id_dsa",
	"home/*/.ssh/id_ecdsa",
	"home/*/.ssh/id_ed25519",
	"root/.netrc",
	"home/*/.netrc",
	"root/.npmrc",
	"home/*/.npmrc",
	"root/.pypirc",
	"home/*/.pypirc",
	"root/.aws/credentials",
	"home/*/.aws/credentials",
	"root/.docker/config.json",
	"home/*/.docker/config.json",
	"root/.config/gcloud/credentials.db",
	"home/*/.config/gcloud/credentials.db",
	"run/secrets/*",
	"var/run/secrets/*",
}

var sensitiveBasenames = map[string]string{
	"shadow":               "unix-password-database",
	"shadow-":              "unix-password-backup",
	"gshadow":              "group-password-database",
	"gshadow-":             "group-password-backup",
	"opasswd":              "old-password-database",
	"id_rsa":               "ssh-private-key",
	"id_dsa":               "ssh-private-key",
	"id_ecdsa":             "ssh-private-key",
	"id_ed25519":           "ssh-private-key",
	".netrc":               "netrc-credentials",
	".npmrc":               "npm-token",
	".pypirc":              "pypi-credentials",
	".pgpass":              "postgres-password",
	".my.cnf":              "mysql-credentials",
	".dockercfg":           "docker-registry-credentials",
	"config.json":          "docker-or-cloud-config",
	"credentials":          "credentials-file",
	"credentials.json":     "credentials-file",
	"service-account.json": "gcp-service-account",
	".env":                 "environment-secrets",
}

// IsSensitiveCredential reports whether a path/content pair resembles a
// credential. The returned string is a stable credential kind.
func IsSensitiveCredential(name string, content []byte) (string, bool) {
	clean, err := CleanPath(name)
	if err != nil {
		clean = strings.TrimLeft(path.Clean("/"+name), "/")
	}
	lower := strings.ToLower(clean)
	base := strings.ToLower(path.Base(lower))

	for _, pattern := range DefaultSensitivePathGlobs {
		if matchPathPattern(pattern, lower) {
			return credentialKindForPath(lower), true
		}
	}
	if kind, ok := sensitiveBasenames[base]; ok {
		// config.json and .env names are common; require either a credential
		// path component or secret content for generic names.
		if base == "config.json" || base == ".env" || base == "credentials" || base == "credentials.json" {
			if looksCredentialPath(lower) || containsSecret(content) {
				return kind, true
			}
		} else {
			return kind, true
		}
	}
	if strings.Contains(lower, ".ssh/") && (strings.HasPrefix(base, "id_") || strings.HasSuffix(base, ".pem")) {
		return "ssh-private-key", true
	}
	if (strings.HasSuffix(lower, ".pem") || strings.HasSuffix(lower, ".key")) && containsPrivateKey(content) {
		return "private-key", true
	}
	if containsSecret(content) {
		return "embedded-secret", true
	}
	return "", false
}

func credentialKindForPath(p string) string {
	switch {
	case strings.Contains(p, "shadow"):
		return "password-hash-database"
	case strings.Contains(p, ".ssh/") && strings.HasPrefix(path.Base(p), "id_"):
		return "ssh-private-key"
	case strings.Contains(p, ".aws/"):
		return "aws-credentials"
	case strings.Contains(p, "docker"):
		return "docker-registry-credentials"
	case strings.Contains(p, "gcloud"):
		return "gcp-credentials"
	case strings.HasPrefix(p, "run/secrets/") || strings.HasPrefix(p, "var/run/secrets/"):
		return "orchestrator-secret"
	default:
		return "credential-file"
	}
}

func looksCredentialPath(p string) bool {
	for _, fragment := range []string{".aws", ".ssh", ".docker", "gcloud", "kube", "secret", "credential"} {
		if strings.Contains(p, fragment) {
			return true
		}
	}
	return false
}

func containsSecret(content []byte) bool {
	if len(content) == 0 || len(content) > 2<<20 {
		return false
	}
	text := string(content)
	return privateKeyPattern.MatchString(text) ||
		awsKeyPattern.MatchString(text) ||
		githubTokenPattern.MatchString(text) ||
		googleAPIKeyPattern.MatchString(text)
}

func containsPrivateKey(content []byte) bool {
	return len(content) > 0 && len(content) <= 2<<20 && privateKeyPattern.Match(content)
}

func matchPathPattern(pattern, target string) bool {
	pattern = strings.TrimLeft(filepathToSlash(pattern), "/")
	target = strings.TrimLeft(filepathToSlash(target), "/")
	if pattern == target {
		return true
	}
	if ok, _ := path.Match(pattern, target); ok {
		return true
	}
	if before, ok := strings.CutSuffix(pattern, "/*"); ok {
		dir := strings.TrimSuffix(before, "/")
		if target == dir || strings.HasPrefix(target, dir+"/") {
			return true
		}
	}
	if before, ok := strings.CutSuffix(pattern, "/..."); ok {
		dir := strings.TrimSuffix(before, "/")
		if target == dir || strings.HasPrefix(target, dir+"/") {
			return true
		}
	}
	if !strings.Contains(pattern, "/") && pattern == path.Base(target) {
		return true
	}
	return false
}
