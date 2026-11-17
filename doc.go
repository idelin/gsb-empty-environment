// Package netsec provides receive-path guards for an IEEE 802.1D spanning
// tree implementation.
//
// Guard compares configuration BPDUs lexicographically by root bridge ID,
// root path cost, sender bridge ID, and sender port ID. Edge and root-guard
// ports reject superior BPDUs and remain non-forwarding for a configured quiet
// period. Topology change notifications are rate-limited per port, and BPDUs
// whose message age has reached their maximum age are discarded.
package netsec
