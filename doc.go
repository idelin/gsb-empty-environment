// Package netsec provides a local, replayable AES-128-CBC padding-oracle
// experiment. It deliberately contains both a legacy padding-feedback
// decryptor and an encrypt-then-MAC decryptor that authenticates before
// attempting decryption.
//
// The package never connects to a remote service and never returns plaintext
// from an oracle callback. RecoverLastBlock only learns the final intermediate
// block through caller-supplied padding feedback and a bounded query budget.
package netsec
