// Package netsec provides scoped, one-time authorization for credit-report
// queries. Grants bind the requester, data subject, purpose, institution, and
// query budget. Successful queries atomically consume a short-lived token and
// are recorded in a tamper-evident, append-only audit ledger.
package netsec
