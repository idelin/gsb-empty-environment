// Package netsec provides the secure-channel state used by a GlobalPlatform
// APDU dispatcher. It enforces monotonic C-MAC/C-DECRYPTION security levels,
// command MAC chaining, replay-resistant counters, and fail-closed handling of
// protected commands.
package netsec
