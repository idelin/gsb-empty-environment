// Package netsec implements RMCP+ cipher suite admission and RAKP
// authentication policy for a simulated IPMI 2.0 BMC.
//
// The admission gate rejects cipher suites whose RAKP authentication or
// confidentiality algorithm is 0, only admits suite numbers registered by the
// caller, rejects anonymous logins, makes unknown users and bad passwords
// indistinguishable at RAKP2, and throttles repeated RAKP failures per source
// address.
package netsec
