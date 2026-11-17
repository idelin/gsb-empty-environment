// Package netsec provides admission checks for containerd certs.d
// hosts.toml mirror configurations plus a digest-enforcing pull helper.
//
// A node configuration service parses a hosts.toml file into a Config and
// calls Validate against its Policy before the file is written to disk.
// Validate rejects:
//
//   - http endpoints and entries with skip_verify enabled, unless the
//     endpoint is loopback and the policy grants the loopback exemption;
//   - Authorization headers on hosts outside the normalized same-domain
//     authority of server (case, default port and trailing DNS dot folded);
//   - pull capability on policy-designated resolve-only mirrors;
//   - unknown/duplicate capabilities, duplicate hosts and partial TLS
//     material references.
//
// Puller consumes an admitted Config to pull content in host order. Blobs
// are streamed through a content digest hasher into a temporary file; when a
// digest-pinned reference returns mismatching bytes, the partial content is
// deleted before the next host is tried, and only verified content is
// atomically renamed into place.
package netsec
