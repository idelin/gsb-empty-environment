// Package netsec provides topic-scoped envelope encryption for a producer
// and matching decryption for a consumer.
//
// The Encryptor rotates a data key after every DefaultMaxMessages messages or
// after DefaultMaxAge, whichever happens first. A message nonce is composed of
// a per-data-key random prefix and a monotonically increasing 8-byte counter.
// The data key is wrapped independently for every registered recipient with
// RSA-OAEP. The Decryptor rejects a message if any key name in its metadata was
// not in the registered recipient set.
package netsec
