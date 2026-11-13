// Package netsec evaluates content-provider path permissions safely.
//
// Request paths are percent-decoded repeatedly until decoding reaches a fixed
// point, then dot segments and duplicate slashes are normalized. Matching,
// including grant-uri-permission matching, is performed only on that canonical
// path. A request that escapes the provider root is rejected before any rule
// can grant access.
//
// Policy evaluation applies every matching rule. Permission requirements are
// joined, so one rule cannot lower the requirement established by another, and
// any matching deny rule wins.
package netsec
