# signify校验和清单文件名绑定

`netsec` 验证 signify 内嵌签名的 SHA256 清单，并供下载完成回调检查已下载文件。

```go
publicKey, err := netsec.LoadPublicKey("release.pub")
if err != nil {
	// handle error
}

manifest, err := netsec.LoadSignedManifest(publicKey, "SHA256.sig")
if err != nil {
	// reject the whole update
}

// In a download completion callback:
if err := manifest.VerifyDownloadFile("update.tar.gz", downloadedPath); err != nil {
	// reject this download
}
```

Verification rules:

- The signature file's first line is an untrusted comment and is ignored.
- The decoded signature record must be `Ed` + 8-byte key number + 64-byte Ed25519 signature. The key number must match the configured public key.
- The embedded manifest body is verified before parsing. Every non-empty body line must have the form `SHA256 (name) = hex`.
- Names containing `/`, `\`, or NUL, and `.` or `..`, invalidate the manifest. Conflicting duplicate entries also invalidate it.
- Download lookups use the exact logical name only. Files absent from the manifest are never accepted.
