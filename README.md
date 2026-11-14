# 指标标签把内存打满了

指标服务接受用户提供的标签值，攻击者为每次请求生成唯一标签，指标基数爆炸耗尽内存并拖慢监控。请实现供Go指标服务调用的可导入标签约束组件。标签键与值按白名单与长度限制，高基数维度拒绝或聚合到桶；每租户的序列数与速率受限。异常基数告警。用唯一标签、超长值、跨租户基数、合法标签和限额验证内存可控。

## netsec 标签约束组件

`netsec` 是纯标准库实现、可直接 import 的 Go 组件，在指标写入前拦截
用户可控标签，保证所有内部状态都有显式上界，内存占用与攻击者输入规模无关。

### 防护能力

- **键白名单**：未在 `Config.Keys` 中声明的标签键直接拒绝（`ErrUnknownKey`）。
- **值长度限制**：`KeyPolicy.MaxLength` 限制字节长度，超长值按策略拒绝
  （`ErrValueTooLong`）或聚合到固定桶 `__other__`。
- **值白名单 / 桶聚合**：白名单内的值原样保留；自由维度以 FNV 哈希映射到
  `BucketCount` 个固定桶，单次请求再怎么唯一也无法产生无限序列。
- **每租户序列上限**：`MaxSeriesPerTenant` 限制不同标签集合（序列）数量，
  超限返回 `ErrSeriesQuota`。
- **每租户速率限制**：令牌桶 `RatePerSecond` / `Burst`，超限返回
  `ErrRateLimited`。
- **租户表上界**：`MaxTenants` 配合 LRU 淘汰，租户数本身也有硬上限。
- **异常基数告警**：`AnomalyWindow` 内新增序列超过阈值即告警；告警进入固定
  容量环形缓冲，通过 `DrainAlerts` 消费，带冷却去重，告警内存同样有界。

### 用法

```go
guard, err := netsec.NewGuard(netsec.Config{
    Keys: map[string]netsec.KeyPolicy{
        "region": {
            MaxLength: 8,
            Whitelist: map[string]struct{}{"us": {}, "eu": {}},
            Policy:    netsec.Reject,
        },
        "request_id": {
            MaxLength:   64,
            Policy:      netsec.Bucket,
            BucketCount: 16, // 该维度最多 16 个序列
        },
    },
    MaxSeriesPerTenant:     1000,
    RatePerSecond:          50,
    Burst:                  100,
    MaxTenants:             10000,
    AnomalySeriesThreshold: 50,
    AnomalyWindow:          10 * time.Second,
})
if err != nil {
    log.Fatal(err)
}

labels, fingerprint, err := guard.Admit(tenantID, map[string]string{
    "region":     "us",
    "request_id":  r.Header.Get("X-Request-ID"),
})
switch {
case errors.Is(err, netsec.ErrSeriesQuota), errors.Is(err, netsec.ErrRateLimited):
    metrics.Dropped.Inc() // 拒绝写入，避免基数爆炸
case err != nil:
    metrics.Invalid.Inc()
default:
    metrics.Requests.With(labelSet(labels)).Inc() // 使用归一化后的标签
}

// 周期性消费告警（缓冲容量固定，不会堆积）
for _, alert := range guard.DrainAlerts() {
    log.Printf("cardinality alert: tenant=%s kind=%s detail=%s",
        alert.Tenant, alert.Kind, alert.Detail)
}
```

### 内存上界

| 资源 | 上界 |
| --- | --- |
| 单租户序列数 | `MaxSeriesPerTenant` |
| 租户数 | `MaxTenants`（LRU 淘汰） |
| 单维度取值数 | 白名单大小 或 `BucketCount (+1)` |
| 单请求标签数 | `MaxLabels` |
| 告警缓冲 | `AlertRingSize`（环形覆盖） |

### 测试

`go test -race ./...` 覆盖：唯一标签洪泛、超长值拒绝/桶聚合、跨租户隔离与
LRU 淘汰、合法标签归一化与指纹稳定性、序列配额、令牌桶速率限制、异常基数
告警与冷却，以及两万次洪泛下的结构上界与堆增长检查。
