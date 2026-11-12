// Package netsec implements admission checks for attacker-supplied BOLT 11
// payment requests. It validates the network prefix, the signature-recovered
// payee key, the payment hash field, the effective expiry, and the cumulative
// fee and CLTV exposure of route hints.
package netsec
