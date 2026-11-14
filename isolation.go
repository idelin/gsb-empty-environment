package netsec

import (
	"net/url"
	"strings"
)

const (
	defaultQueueName = "default"
	invalidDomain    = "_invalid_domain_"
)

func escapeSegment(value string) string {
	escaped := url.PathEscape(value)
	if escaped == "" {
		return "%00"
	}
	// Dots are escaped as well so that no namespace component can be
	// normalised into a "." or ".." traversal by a downstream consumer.
	return strings.ReplaceAll(escaped, ".", "%2E")
}

func scopePrefix(kind string, domain TrustDomain) string {
	token := string(domain)
	if token == "" {
		token = invalidDomain
	}
	return kind + "/" + escapeSegment(token)
}

// QueueName returns a queue name scoped to the domain. Two domains can never
// share the same queue, even if jobs use the same logical queue name.
func QueueName(domain TrustDomain, queue string) string {
	if queue == "" {
		queue = defaultQueueName
	}
	return scopePrefix("queue", domain) + "/" + escapeSegment(queue)
}

// CacheNamespace returns a cache namespace scoped to the domain and
// repository. The result is safe to use as an opaque namespace key.
func CacheNamespace(domain TrustDomain, repository, key string) string {
	if key == "" {
		key = defaultQueueName
	}
	return scopePrefix("cache", domain) + "/" + escapeSegment(repository) + "/" + escapeSegment(key)
}

// ArtifactNamespace returns an artifact namespace scoped to the domain and
// repository. The result is safe to use as an opaque namespace key.
func ArtifactNamespace(domain TrustDomain, repository, key string) string {
	if key == "" {
		key = defaultQueueName
	}
	return scopePrefix("artifacts", domain) + "/" + escapeSegment(repository) + "/" + escapeSegment(key)
}
