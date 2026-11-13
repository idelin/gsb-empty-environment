// Package netsec implements OpenPGP v2 SEIPD chunked AEAD message
// encryption and decryption.
//
// A message is split into fixed-size plaintext chunks. Each chunk is sealed
// with AES-GCM using a nonce derived from the message IV and the chunk
// sequence number, and with associated data that binds the packet version,
// algorithms, chunk size, and sequence number. The last chunk uses
// FinalSequence (all ones) instead of its ordinal.
//
// The default sequence policy, AllowSequenceReuse, reproduces the reported
// chunk-sequence bug: a non-final chunk may repeat the sequence number of the
// preceding chunk. Reusing a sequence number reuses both the AEAD nonce and
// associated data, so two chunks sealed under that sequence are
// interchangeable. In particular, moving an earlier chunk's tag and
// ciphertext into a later reused position makes the later position decrypt to
// the earlier plaintext.
//
// RequireSequential enables strict validation: the normal chunks must be
// numbered 0, 1, 2, ... with no repeats, and exactly one final marker must
// terminate the message. In both policies, sequence jumps, backwards moves,
// and duplicate final markers are rejected.
//
// The package is importable by mail-decryption callers:
//
//	message, err := netsec.EncryptMessage(sessionKey, plaintext, netsec.Config{
//	    ChunkSize: 1 << 20,
//	})
//	...
//	plaintext, err := message.Decrypt(sessionKey)
package netsec
