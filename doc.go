// Package netsec checks algorithm-negotiation transcripts for downgrade
// attacks.
//
// A transcript is an ordered sequence of captured messages (capability
// announcements, a selection and final confirmations). Every message is
// presented together with its raw bytes and an authentication mark produced
// by the Go verification host. A message only proves anything when its mark
// is valid and its signer matches the claimed sender: unauthenticated
// capability claims are reported as clues and never prove that a peer
// supports an algorithm.
//
// The check enforces that the selected algorithm belongs to the intersection
// of both peers' authenticated capabilities and honors the caller-supplied
// preference order. Confirmations must be authenticated, come from both
// peers and bind the selected algorithm to the transcript digest, which is
// what detects cross-session transcript splicing.
package netsec
