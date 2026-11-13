// Package netsec implements the delegation core of a GNUnet GNS-style name
// resolver.
//
// Resolution walks a name from right to left. The right-most (root) label is
// looked up only in the local ego table, which maps the label to a zone public
// key; no record served by a remote zone can ever explain or shadow it. Each
// remaining label is looked up in the current zone block:
//
//   - A PKEY record switches resolution to another zone. At most
//     MaxDelegationDepth switches are allowed and a zone key that repeats along
//     the chain is treated as a loop.
//   - A CNAME record restarts resolution under an alias name.
//   - A label block carrying both a live PKEY and a live CNAME is invalid.
//   - Records whose absolute expiration is at or before the current instant
//     are ignored.
//   - Deleting an ego makes every name rooted at that label fail immediately.
package netsec
