// Package netsec implements a crash-safe PIN/PUK verification handler for a
// smart-card reader simulator.
//
// A PIN or PUK attempt decrements its durable retry counter and commits the
// decrement to a two-slot journal before the secret is compared. Only a
// successful comparison restores a budget. Power loss can therefore reduce
// availability, but cannot create an extra comparison.
package netsec
