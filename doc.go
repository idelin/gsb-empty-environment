// Package netsec implements admission-time source policy checks for BuildKit
// solve requests. It canonicalizes image, git, HTTP, and local source
// identities, validates mutable or credential-bearing references, and applies
// an ordered first-match allow/deny/convert rule set.
package netsec
