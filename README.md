# CDS接纳一致性与删除信号

子区被攻陷的某台权威服务器可能注入 CDS，想让父区替换 DS。为Go注册局组件写可导入的 CDS/CDNSKEY 接纳判定：只接受当前 DS 所指 KSK 签名的记录；全部权威服务器返回的 CDS 集须一致且连续观察满配置的小时数；CDS 与 CDNSKEY 并存时须互相对应；删除记录为 0 0 0 00 且不得与别的记录混放；新 DS 须对应已发布的 DNSKEY。测单台偏离、签名钥不符、混放删除、观察中断。

## 用法

```go
acceptor, err := netsec.NewAcceptor(netsec.Config{
    ObservationPeriod: 24 * time.Hour,
    // 可选：两次成功观测间隔超过该值即视为中断。
    MaxObservationGap: time.Hour,
})
if err != nil {
    return err
}

result := acceptor.Observe(netsec.Request{
    Zone:      "child.example.",
    CurrentDS: currentParentDS,
    Servers: []netsec.ServerResponse{
        {
            Server:            "ns1.child.example.",
            CDS:               cdsRRset,
            CDSSignatures:     cdsRRSIGs,
            CDNSKEY:           cdnskeyRRset,
            CDNSKEYSignatures: cdnskeyRRSIGs,
            DNSKEY:            dnskeyRRset,
            DNSKEYSignatures:  dnskeyRRSIGs,
        },
        // 必须包含该子区的全部权威服务器。
    },
})

switch result.Status {
case netsec.StatusAccepted:
    if result.Delete {
        // 删除子区 DS。
    } else {
        // 用 result.NewDS 替换父区 DS。
    }
case netsec.StatusPending:
    // 候选仍在连续观察窗口内。
case netsec.StatusRejected:
    // result.Reason 说明拒绝原因；连续观察窗口已重置。
case netsec.StatusNoSignal:
    // 所有服务器一致地未发布 CDS/CDNSKEY。
}
```

`ServerResponse.Err` 表示查询失败。任一权威服务器失败都会使本轮观测无效并
重置连续窗口，不会把缺失服务器当作“同意”。

## 判定规则

- 只用父区当前 DS 摘要匹配出的 KSK 验证 DNSKEY、CDS、CDNSKEY 的 RRSIG；
  DNSKEY 必须带 zone key 与 SEP 标志，CDS/CDNSKEY 的签名者也必须匹配当前 DS。
- 所有服务器的规范化 CDS 集必须完全相同；存在 CDNSKEY 时其集合也必须一致。
- 同一候选必须连续观察满 `ObservationPeriod`。候选变化、服务器错误、
  no-signal 轮次、可选的最大观测间隔超限或时钟回退都会重置窗口。
- CDS 与 CDNSKEY 同时出现时双向对应：每条 CDS 都能由某条 CDNSKEY 导出，
  每条 CDNSKEY 也至少对应一条 CDS。
- 每条 CDS 必须对应一条已发布且已通过当前 KSK 验证的 DNSKEY，且该 DNSKEY
  是 KSK。
- 删除信号必须精确为 `0 0 0 00`，删除 RRset 中不得混入其他 CDS，也不得与
  CDNSKEY 并存。
- 仅发布 CDNSKEY 时，按 `Request.CDNSKEYDigestTypes`（默认 SHA-256）导出 DS。

## 验证

```sh
go test -race ./...
```

测试使用真实 ECDSA P-256 KSK 签名，覆盖单台服务器 CDS 偏离、签名密钥不符、
删除与更新记录混放、观测中断后窗口重置，以及新 DS 未对应已发布 DNSKEY 和
CDS/CDNSKEY 不对应等场景。
