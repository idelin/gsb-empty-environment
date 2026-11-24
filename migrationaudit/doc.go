// Package migrationaudit plans and verifies password-digest migration orders
// without accepting passwords or other secret material.
//
// Every input uses finite identifiers only. Digests declare a salt domain,
// password family, cost, and the accounts that can verify them directly.
// Observer groups remember every digest replica they can read, including a
// digest read through an old coalition policy immediately before narrowing.
// A low-cost digest exposes its direct accounts and every account in the same
// password family and salt domain at the observed minimum cost. Salt domains
// are tracked independently.
//
// Solve minimizes, in order, the worst single-group exposure peak across all
// non-empty prefixes, the sum of worst single-group prefix exposures, and the
// lexicographic sequence of action IDs. VerifyResult independently replays the
// returned state certificate instead of trusting the solver's search result.
package migrationaudit
