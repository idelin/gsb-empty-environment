# 有效位与清晰位

证书的关键选项区被改了一位，签名仍因覆盖范围不含该区而通过。签名输入必须包含关键选项、扩展、主体和时间；任一区变化都要失败。原始证书签名通过。可导入的Go库供SSH服务调用。改动force-command的一个字节再验原签名。

## 用法

```go
certLine, _ := os.ReadFile("id_ed25519-cert.pub")
caLine, _ := os.ReadFile("ca.pub")

if err := netsec.VerifyCertificate(certLine, caLine); err != nil {
	// 证书签名无效
}
```

也可以先解析，再对解析后的对象验签：

```go
cert, err := netsec.ParseCertificate(certLine)
if err == nil {
	err = cert.VerifySignature(caLine)
}
```

`VerifyCertificate` 接受二进制证书 blob、authorized_keys 证书行、
`*netsec.Certificate`，以及带 `Marshal() []byte` 的 `*ssh.Certificate`。
CA 参数接受 key blob、authorized_keys 行、PEM 公钥、标准库公钥，
以及 `ssh.PublicKey`。

## 覆盖范围

签名输入是证书 wire format 中从类型字段到签名公钥字段（含）的完整字节前缀：

1. 证书类型、nonce、被签公钥
2. serial、证书类型值、key id
3. valid principals、valid after、valid before
4. critical options（含 `force-command`）、extensions、reserved
5. signature key

签名本身不参与验签。解析后的导出字段会重新编码并与原始签名前缀比较，
因此解析后修改 `CriticalOptions`、`Extensions`、`ValidPrincipals`
或有效时间同样会失败。

## 验证

```sh
go test ./...
```

测试覆盖 ed25519、RSA（SHA-256/SHA-512）、ECDSA P-256，
以及由真实 `ssh-keygen` 生成的 ed25519/RSA/ECDSA 证书。
其中包含对 `force-command` 单个字节的篡改测试。
