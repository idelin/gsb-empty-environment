# knownhosts

运维平台的Go SSH客户端要按OpenSSH的known_hosts核对主机密钥。实现可供连接回调调用的匹配包：支持哈希主机名、通配与否定模式、[host]:port形式、@cert-authority和@revoked标记；吊销记录优先于任何匹配，同一主机出现不同密钥时报告冲突，不静默接受。用混合多种格式的文件与多组主机、端口、密钥组合测试每个决策。

## 实现

`package knownhosts` 仅依赖标准库，解析 OpenSSH `known_hosts` 并提供主机密钥校验回调。

- `New(path)` / `NewFromReader(r)`：解析文件或读取器，首个非法行返回带行号的 `*ParseError`。
- `(*KnownHosts).HostKeyCallback(hostname, port, key)`：核心决策函数；`Callback()` 返回同签名的函数值。
- `PublicKey` 是最小接口（`Type()` 与 `Marshal()`），`golang.org/x/crypto/ssh.PublicKey` 直接满足；适配 `ssh.ClientConfig`：

```go
kh, err := knownhosts.New("known_hosts")
cfg.HostKeyCallback = func(host string, _ net.Addr, key ssh.PublicKey) error {
	hostname, port, _ := net.SplitHostPort(host)
	n, _ := strconv.Atoi(port)
	return kh.HostKeyCallback(hostname, n, key)
}
```

## 支持的记录形式

- 明文主机名、逗号分隔的多主机列表；`*` / `?` 通配与 `!` 否定模式（同一行内“至少一个正向命中且无否定命中”才算匹配）。
- `[host]:port`（含 IPv6 字面量）与裸 `host:port`；`:*` 表示任意端口，未限定端口的模式匹配任意端口。
- 哈希主机 `|1|<salt>|<hash>`（HMAC-SHA1，按 `[host]:port` 规范化地址计算），可用 `HashHostname` 生成；同时兼容带/不带 `=` 填充的 base64。
- `@cert-authority`：行内密钥作为 CA，校验主机证书的签发 CA、principal 列表、有效期与签名（Ed25519 / RSA / ECDSA）。
- `@revoked`：吊销优先级最高——被吊销的密钥（或证书的签发 CA）一律返回 `*RevokedError`，即使另有可接受的行；`@revoked host *` 吊销该主机的任意密钥类型。
- 未知 `@marker` 与 OpenSSH 一样忽略，不中断解析。

## 决策与错误

- 主机无任何匹配行：`*KeyError`（no known host key）。
- 主机有记录但密钥不一致：`*KeyError`（host key mismatch），`Want` 给出已记录密钥及行号、`Got` 给出服务器提供的密钥，绝不静默接受。
- 吊销命中：`*RevokedError`（含行号与密钥类型），优先于所有其它结果。
- 证书无法通过 CA 锚定、principal、有效期或签名校验：`*KeyError`。

## 测试

```sh
go test -race -cover ./...
```

测试使用混合格式的 `known_hosts`（注释、空行、明文、括号端口、裸 `host:port`、IPv6、通配、否定、多模式、哈希主机、多密钥、CA、吊销、未知 marker），配合多组主机/端口/密钥与自签名 Ed25519、RSA、ECDSA 证书，对每个决策分支（接受、冲突、吊销、证书失败、解析错误、并发回调）做表驱动覆盖。
