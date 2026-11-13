// Package netsec verifies certificate paths, including RFC 5280 style
// name constraints with the self-issued exemption: a self-issued
// intermediate certificate (identical subject and issuer, signed with
// the same key it certifies) skips the name-constraint check exactly
// once, for itself. Constraints accumulated above it remain in force
// for every certificate below it, so a self-issued certificate can
// neither be wrongly rejected as a cross-certificate nor be abused to
// wash constraints away.
package netsec
