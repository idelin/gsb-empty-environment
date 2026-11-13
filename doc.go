// Package netsec implements a dual-control HSM root-key import ceremony.
//
// Administrator credentials are authenticated by an HR root key and deduplicated
// by immutable person ID. Ceremony approvals and sealed key shares are bound to
// a random ceremony ID and a complete target/policy binding. The HSM receives
// only sealed shares and returns a signed receipt that must match the binding
// before the ceremony is considered complete.
package netsec
