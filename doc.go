// Package netsec provides SCRAM channel binding policy checks for a Go
// authentication server.
//
// EvaluateFirstMessage validates the selected SCRAM mechanism against the
// client's GS2 channel binding flag and the mechanisms advertised by the
// server. It rejects SCRAM channel binding downgrade, mechanism/GS2
// mismatches, and unsupported p= channel binding types. Callers attach the
// TLS channel binding data with ChannelBinding.WithBindingData and then call
// ChannelBinding.VerifyFinalMessage to validate the client's c= attribute.
//
// For tls-server-end-point, TLSServerEndPointBindingData hashes the
// certificate's DER bytes with the hash used by the certificate signature
// algorithm. MD5 and SHA-1 are upgraded to SHA-256 as required by RFC 5929.
package netsec
