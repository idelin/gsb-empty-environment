// Package netsec implements target-side mutual CHAP for iSCSI login.
//
// An Authenticator shares a registry of target-issued challenges across all
// login connections. Reusing a target challenge as the initiator's reverse
// challenge is rejected, and the forward and reverse secrets are required to
// differ. Responses are MD5(identifier || secret || challenge) and can be
// exchanged in 0x-prefixed hexadecimal or standard base64 form.
package netsec
