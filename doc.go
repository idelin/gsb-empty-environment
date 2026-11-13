// Package netsec implements pre-signing policy checks for BIP174 PSBTs.
//
// The policy never trusts PSBT-declared input amounts or change metadata.
// Key derivation and prevout lookup are injected by the caller, so the
// package can sit in front of hardware or software signing without holding
// private keys or network access.
package netsec
