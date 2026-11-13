# AIA只追有限跳

颁发者地址指向file或循环的http，路径构建会一直取下去。AIA只允许http和https，最多追调用方给定的跳数；循环或超跳失败。一步https就能补齐颁发者的路径可以继续。证书路径调用可导入的Go库做这次裁决。给出file地址和一条三跳循环。

## 用法

```go
roots := x509.NewCertPool() // 装入信任锚
b := &netsec.Builder{Roots: roots, MaxHops: 4}
chains, err := b.BuildPath(ctx, leaf)
```

- 只跟随 `http`/`https` 的 caIssuers 地址；`file://` 等其他 scheme 报 `ErrSchemeNotAllowed`，绝不抓取。
- 抓取次数不超过调用方给的 `MaxHops`，超出报 `ErrMaxHops`；`MaxHops` 为 0 时完全不联网。
- 同一地址只抓一次，再次遇到（如三跳循环 ca1→ca2→ca3→ca1）报 `ErrLoopDetected`。
- 抓回的证书与当前证书颁发者名称不符报 `ErrIssuerMismatch`；最终路径裁决交给 `crypto/x509` 的 `Verify`。
