// Package netsec implements the TLS 1.3 exporter key schedule with an
// explicit early versus post-handshake phase distinction.
//
// The early exporter is keyed by early_exporter_master_secret, derived from
// the early secret with the "e exp master" label. The post-handshake exporter
// is keyed by exporter_master_secret, derived from the handshake master secret
// with the "exp master" label. The two secrets and the two exported byte
// strings are intentionally distinct and cannot be substituted for each other.
package netsec
