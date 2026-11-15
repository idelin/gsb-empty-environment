// Package netsec evaluates the offline-tamper protection provided by
// security.evm before a file is accessed.
//
// Evaluation is fail-closed. Missing keys, missing or malformed security.evm,
// unknown type bytes, failed HMAC comparisons, and failed portable signature
// checks all deny access. security.ima is authenticated as EVM input data and
// is never used as a fallback when EVM cannot be verified.
package netsec
