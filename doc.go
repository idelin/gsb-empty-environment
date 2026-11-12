// Package netsec converges security-service handshake failures onto a
// small, constant-size public error shape so that error length cannot
// reveal which internal authentication stage failed.
//
// Detailed failure categories and secret-bearing context stay inside the
// process: internal errors render as one fixed string under every fmt verb,
// while Publicize maps each internal stage through an allow-list onto one
// of four fixed-width public categories. The resulting PublicError has
// constant-length text, JSON, and wire encodings.
//
// Guard enforces the operational boundary for one host-facing connection:
// at most one failure is reported, the redacting logger and failure
// callback each run at most once per reported failure, and Close runs at
// most once even under concurrent invocation.
package netsec
