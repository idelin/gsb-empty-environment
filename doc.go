// Package netsec provides admission checks for one-way Binder transactions.
//
// A Checker enforces a shared one-way buffer limit and a per-caller pending
// limit. Transactions are accounted for until their completion event is
// delivered. Offset arrays are validated before any quota is reserved.
package netsec
