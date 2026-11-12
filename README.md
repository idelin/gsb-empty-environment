# CDS接纳一致性与删除信号

子区被攻陷的某台权威服务器可能注入 CDS，想让父区替换 DS。为Go注册局组件写可导入的 CDS/CDNSKEY 接纳判定：只接受当前 DS 所指 KSK 签名的记录；全部权威服务器返回的 CDS 集须一致且连续观察满配置的小时数；CDS 与 CDNSKEY 并存时须互相对应；删除记录为 0 0 0 00 且不得与别的记录混放；新 DS 须对应已发布的 DNSKEY。测单台偏离、签名钥不符、混放删除、观察中断。

## 策略到实现

所有接纳逻辑集中在可导入包 `netsec`（无第三方依赖），入口为 `NewTracker` + `Tracker.Observe`。

| 安全要求 | 实现位置 |
| --- | --- |
| 仅接受当前 DS 所指 KSK 的 RRSIG | `cds.go` `validateSignedSet`；密码学原语在 `dnssec.go`（RFC 4034 §3.1 签名输入、RDATA 规范排序） |
| 全量权威服务器 CDS/CDNSKEY 集一致 | `cds.go` `validatePoll` + `structure.go` `fingerprint`（带类型前缀的规范指纹） |
| 连续观察满配置小时数，中断/失败/时钟漂移即重新计时 | `cds.go` `Observe`，阈值取 `Config.RequireHours`、`PollInterval`、`MaxClockSkew` |
| CDS 与 CDNSKEY 并存须互相对应 | `structure.go` `validateCorrespondence`（用 CDS 摘要类型从 CDNSKEY 完整推导 DS 后逐字节比较） |
| 删除信号 0 0 0 00 且不得混放 | `structure.go` `validateCDSStructure` / `validateCDNSKEYStructure` |
| 新 DS 须对应已发布 DNSKEY | `cds.go` `dsMatchesPublished` / `dnskeyPublished`；DS 摘要按 RFC 4509 重算，不信任 keytag |

支持算法：RSA/SHA-1/256/512（5/8/10）、ECDSA P-256/P-384（13/14）、Ed25519（15）；DS 摘要支持 SHA-1/SHA-256/SHA-384。

## 使用方式

```go
tr, err := netsec.NewTracker(netsec.Config{RequireHours: 24}, parentDS, publishedSigners)
// 每小时轮询全部权威服务器一次：
decision, err := tr.Observe(netsec.Poll{Zone: "example.test.", At: now, Servers: obs})
switch decision.Action {
case netsec.ActionReplace: // 用 decision.NewDS 替换父区 DS
case netsec.ActionDelete:  // 移除父区 DS
case netsec.ActionPending: // err 携带拒绝规则，errors.Is 匹配 ErrServerDivergence 等哨兵
}
```

任一校验失败都会清空连续观察状态；恢复合规后必须重新积累满 `RequireHours` 小时。

## 测试

```sh
go test ./... -race -count=1
```

场景覆盖：单台服务器偏离、签名钥未被当前 DS 锚定/非 KSK、RRSIG 篡改与过期、删除信号混放与畸形、观察间隔超窗/失败轮/时钟漂移/信号内容变化、CDS/CDNSKEY 不对应、新 DS 无已发布 DNSKEY，以及 RSA/ECDSA/Ed25519 三种签名算法的完整签名链。
