# TLS 1.3 CertificateVerify context

RFC 8446, Section 4.4.3, signs this exact byte string for a TLS 1.3
CertificateVerify message:

```
64 * 0x20 || context label || 0x00 || Transcript-Hash
```

The context labels are:

- server: `TLS 1.3, server CertificateVerify`
- client: `TLS 1.3, client CertificateVerify`

The 64 leading space octets are part of the signed input. This package never
trims, repairs, or normalises a supplied context: a context with only 63
spaces is rejected, even if its signature is cryptographically valid over the
shortened input.

## Usage

```go
message, err := netsec.SignedMessage(netsec.RoleServer, transcriptHash)
if err != nil {
    return err
}

err = netsec.VerifySignedMessage(publicKey, message, signature, &netsec.VerifyOptions{
    Scheme: netsec.PSSWithSHA256,
})
```

`VerifyCertificateVerify` builds the context from the role. `VerifyContext`
accepts a caller-supplied context and validates it byte-for-byte:

```go
err := netsec.VerifyContext(publicKey, context, transcriptHash, signature, opts)
if errors.Is(err, netsec.ErrInvalidContext) {
    // The context is not exactly 64 spaces plus the server/client label.
}
```

`Verify` and `Sign` are argument-order-tolerant convenience dispatchers.
The typed helpers should be preferred by the TLS stack.

Run `go test ./...`; `TestOneMissingSpaceIsRejected` signs a malformed
63-space input and confirms that verification still fails.
