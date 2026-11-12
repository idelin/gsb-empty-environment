// Package netsec verifies DSSE-wrapped in-toto statements for artifact
// admission. It binds the signed payload type, artifact digest, source
// repository, commit, builder identity, and X.509 certificate identity before
// invoking a publish callback.
package netsec
