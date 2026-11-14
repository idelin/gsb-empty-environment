// Package netsec implements policy-controlled LDAP referral following for a
// directory gateway.
//
// Referral URLs are parsed into scheme, host, port, and DN. Credentials are
// forwarded only to hosts inside the configured trust domain, on an allowed
// port, when credential forwarding is enabled. Other followable referrals use
// an anonymous simple bind. LDAPS and StartTLS validate the certificate and
// host identity before any bind; cleartext downgrade is refused. Referral
// depth, cycles, and forest boundaries are bounded explicitly, and every
// decision and bind is reported to an AuditSink.
package netsec
