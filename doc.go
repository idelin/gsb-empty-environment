// Package netsec is an importable export-policy checker for boundary
// routers. It models publication policies as ordered rule lists with
// conditional branches, community tag add/delete actions, accept/reject
// verdicts and non-recursive sub-policy calls, then exhaustively checks
// a bounded finite domain for routes that leak a confidential tag to
// forbidden neighbors.
//
// The universe is limited to MaxTags (16) community tags, so tag sets
// form a 2^16 finite domain. Prefix and path-length conditions induce
// atomic numeric intervals; one representative per interval covers
// every possible condition outcome, including outcomes after tag
// mutations and across early accept/reject returns. CheckLeak returns
// the minimal leaking route (deterministic total order) together with
// the full per-rule execution Trace, renderable via RenderTrace.
package netsec
