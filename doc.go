// Package netsec implements ISO/IEC 18013-5 mdoc reader authentication for
// wallets: it checks that the reader certificate carries the reader extended
// key usage and that the reader authentication signature covers a session
// transcript containing the handover, the reader key and the device key.
package netsec
