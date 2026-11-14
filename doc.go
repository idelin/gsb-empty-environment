// Package netsec resolves OCI and Docker multi-platform images without
// trusting mutable tags, annotations, or unverified child manifests.
//
// A Resolver first maps a tag to an immutable root digest and verifies the
// root document. For an index it selects exactly one platform, verifies the
// selected child manifest against the descriptor in the index, then verifies
// the config and every layer against their descriptors. Verified content is
// cached by digest only, so moving a tag cannot reuse a stale tag-to-digest
// mapping.
package netsec
