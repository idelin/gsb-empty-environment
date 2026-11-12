// Package taskpkg plans atomic firewall updates across administrative regions.
//
// A region owns a set of mutable filter edges. In one rollout step a region
// replaces that complete set with one of its installable versions. A plan is
// accepted only when every configuration reachable from the issued steps keeps
// all attack entries separated from the secret nodes while preserving every
// authenticated management route.
//
// The default verification is deliberately adversarial: receipts may arrive
// out of order and a region may become unreachable, so every subset of issued
// updates is considered to be a possible installed configuration. Regions
// that cannot confirm an installation are also checked in both their old and
// new forms. If no complete safe rollout exists, Plan returns ErrNoSafeOrder
// and no first step.
package taskpkg
