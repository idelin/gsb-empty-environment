// Package netsec builds and executes host-controlled layer fetch plans for
// OCI image manifests.
//
// A manifest identity is supplied by the image host and cannot be changed by
// layer URLs. Layer bytes are written to an isolated partial file, validated
// against the declared compressed size and sha256 digest, and only then
// published to a digest-keyed cache of verified blobs. Redirects are followed
// manually by the host so that URL policy, credentials, cookies, client
// certificates, and custom headers are recomputed for every hop.
package netsec
