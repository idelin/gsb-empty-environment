// Package netsec implements authenticated, chunked chat attachments.
//
// A message attachment is split into fixed-size plaintext chunks. Every chunk
// is encrypted with AES-256-GCM using a nonce derived from the attachment's
// random file key, file identifier, and chunk index. The AEAD associated data
// additionally binds the chunk index, file identity, total size, and metadata
// digest. This prevents ciphertext from one attachment, or from another
// position in the same attachment, being substituted for a legitimate chunk.
//
// The manifest contains the digest of every ciphertext chunk, a Merkle root,
// and one file-key envelope per recipient device. The sender signs the entire
// manifest with Ed25519. Opening verifies the signature and recipient envelope
// before any chunk is decrypted.
package netsec
