// Package netsec provides a code-page retirement component for a dynamic code
// runtime.
//
// Before an executable page may be made writable, the Coordinator requires a
// proof: every registered thread reports at a safepoint against the same
// barrier generation, and neither its executing instruction interval nor any
// return address on its stack references the target page. Until that proof
// exists the page stays read-only executable. Stalled threads, reports from a
// stale generation, and threads that join while a barrier is open are all
// accounted for and can never be silently skipped.
package netsec
