# SSH主机密钥轮换通告的证明

一个无外部依赖的 Go 包，用于处理 OpenSSH 的
`hostkeys-00@openssh.com` 主机密钥通告。它把通告视为不可信输入，并且在
所有证明通过前不会修改 `known_hosts`。

## 安全规则

- 只有调用方明确标记 `HostAuthenticated`，并且 `known_hosts` 已存在该主机
  的普通（非 `@revoked` / `@cert-authority`）条目时才处理。
- 新通告的密钥必须由 `hostkeys-prove-00@openssh.com` 返回的签名证明。
- 签名数据严格为 `string(request name) || string(session ID) ||
  string(public key blob)`。
- 服务器不再列出的旧密钥，只会在同一批可验证的证明成功后才删除。若没有新增
  密钥但有删除操作，则要求证明所有仍被列出的密钥。
- 任意一个密钥缺失证明、签名属于其他会话、签名错误、响应数量不匹配或密钥被
  `@revoked` 拒绝，整批都不写入。
- `known_hosts` 使用同目录临时文件和 `rename` 原子替换，并保留文件权限。

## 使用

```go
request := netsec.Request{
    Host:              knownHostsHost, // 例如 example.com 或 [example.com]:2222
    KnownHostsPath:    knownHostsPath,
    SessionID:         sshConn.SessionID(),
    HostAuthenticated: true, // 仅在主机密钥校验成功后设置
    Payload:           notificationPayload,
}

result, err := netsec.Handle(request, netsec.ConnProver{Conn: sshConn})
```

`ConnProver` 使用一个批量请求发送全部待证明密钥，并按顺序读取
`string[] signatures`。也可以实现 `Prover`，或使用 `ProverFunc` /
`SingleProverFunc` 适配现有传输层。

包支持 Ed25519、RSA（`ssh-rsa`、`rsa-sha2-256/512`）、ECDSA
（nistp256/384/521）和 DSA 证明验证。`known_hosts` 支持普通主机名、
通配符、否定模式和 `|1|...` 哈希主机名；如果删除会因通配符影响其他主机，
操作会以 `ErrAmbiguousKnownHostsEntry` 拒绝，而不是扩大影响范围。

## 测试

```sh
go test ./...
go test -race ./...
```
