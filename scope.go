package sitemapdisc

import "net/url"

// resolveChild resolves loc against the parent sitemap URL and enforces the
// containment policy: the reference must use http(s), keep the exact host
// (including port) of the parent and have a path located in the parent's
// directory. Relative references are resolved before the check.
func resolveChild(parent *url.URL, loc string) (*url.URL, bool) {
	ref, err := url.Parse(loc)
	if err != nil {
		return nil, false
	}
	child := parent.ResolveReference(ref)

	if !isHTTP(child) || child.Host == "" {
		return nil, false
	}
	// Hostname and port must both match; ResolveReference keeps the parent
	// host for relative references, so this only rejects explicit escapes.
	if child.Hostname() != parent.Hostname() || child.Port() != parent.Port() {
		return nil, false
	}

	dir := parentDir(parent.EscapedPath())
	childPath := child.EscapedPath()
	if childPath == "" {
		childPath = "/"
	}
	if childPath != dir && !pathHasPrefix(childPath, dir) {
		return nil, false
	}
	return child, true
}

// parentDir returns the directory portion of a sitemap path, always with a
// trailing slash. "/sitemaps/a.xml" -> "/sitemaps/", "/a.xml" -> "/".
func parentDir(p string) string {
	if p == "" || p[len(p)-1] == '/' {
		if p == "" {
			return "/"
		}
		return p
	}
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[:i+1]
		}
	}
	return "/"
}

// pathHasPrefix reports whether path starts with the slash-terminated prefix,
// so "/sitemap.xml" does not pass the prefix "/sit".
func pathHasPrefix(path, prefix string) bool {
	if len(path) < len(prefix) || path[:len(prefix)] != prefix {
		return false
	}
	return prefix[len(prefix)-1] == '/'
}
