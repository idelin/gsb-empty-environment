# dns-replay

离线、确定性地重放内网服务在 DNS 缓存、SSRF 校验、拨号与连接池复用上的
完整事件序列，定位“校验通过却实际连入禁用网段”的请求，并给出可独立重放的
因果事件子序列。全程使用虚拟时钟，不发起任何真实网络请求。

## 建模的三类地址

每个请求在报告中都能区分三个地址概念：

- `validated_addresses`：SSRF 校验时刻解析到并用于网段检查的地址；
- `dial_address`：池未命中、真正拨号时按当时解析结果选用的地址（取首个）；
- `reused_peer`：池命中时复用的既有连接对端，复用时不再重新解析或校验。

## 输入场景

```json
{
  "forbidden_cidrs": ["10.0.0.0/8", "169.254.169.254/32"],
  "hosts": {
    "svc.internal": {
      "port": 8080,
      "ttl": 10,
      "resolution_steps": [
        ["172.16.0.10"],
        ["10.20.30.40"]
      ]
    }
  },
  "requests": [
    {"id": "r1", "host": "svc.internal", "validate_at": 5, "dial_at": 15}
  ]
}
```

- `forbidden_cidrs`：禁用网段，支持 IPv4/IPv6，使用 `net/netip` 判定包含关系。
- `hosts.*.ttl`：每次解析结果写入缓存后的存活时间（虚拟时间单位）。
- `hosts.*.resolution_steps`：脚本化解析结果，每次缓存失效后的重新解析按顺序
  消费一个 step；同一 step 可给多个地址，校验会逐一检查，任一命中即 BLOCK。
- `requests[].validate_at` / `dial_at`：校验与拨号时刻，用来表达 TOCTOU 窗口；
  `dial_at` 缺省等于 `validate_at`，请求级 `port` 可覆盖主机默认端口。

## 事件与 TTL 语义

事件类型：`dns_timer_expiry`、`dns_cache_lookup`、`dns_resolve`、
`dns_cache_store`、`ssrf_check`、`pool_miss`、`pool_get`、`dial`、
`pool_put`、`violation`。

- TTL 采用包含式到期：条目在 `stored_at + ttl` 时刻即失效，该时刻的查找必然
  未命中并触发重新解析（`dns_timer_expiry` 先于同刻查找触发），专门覆盖
  “TTL 恰好到期”。
- 每个事件带全局序号、虚拟时间、向量钟与显式因果父边；事件按全局序拓扑排列。
- 连接池以 `host:port` 为键，池内连接保留实际对端；池命中优先于拨号，且命中时
  不会重新解析。

## 两类违规

- `V1_DIAL_FORBIDDEN_AFTER_PASS`：校验 PASS，TTL 到期后重新解析得到禁用地址，
  池未命中并拨号到该地址。
- `V2_POOL_PEER_FORBIDDEN_AFTER_PASS`：校验 PASS（可能基于重新解析回的安全
  地址），但池命中复用了先前建立、对端位于禁用网段的连接。

仅当 `ssrf_check` 为 PASS 且后续网络动作（拨号或复用）落到禁用网段时才计违规；
校验阶段直接 BLOCK 的请求不会拨号，也不算违规。

## 因果子序列与独立重放

每个违规沿显式父边做传递闭包，得到按全局序排列的最小因果事件子序列，并投影出
一个自包含 `Scenario`：保留闭包涉及的全部请求（维持原始顺序与时间戳），并把
每个主机的解析脚本裁剪到实际用到的 step。该子场景可直接独立重放并复现同一违规：

```sh
go run . run --scenario testdata/v2_pool_reuse.json --report report.json
jq '.violations[1].subscenario' report.json > witness.json
go run . replay --scenario witness.json
```

`run` 把 JSON 报告写到 stdout（或 `--report` 指定文件），人类可读摘要写到 stderr；
`replay` 在子场景无法复现违规时以非零状态退出。

## 测试

```sh
go test ./...
```

测试覆盖 TTL 恰好到期的 V1、跨请求投毒后池复用的 V2、BLOCK 与安全拨号分支、
多地址逐一校验、池键按 `host:port` 隔离，以及提取出的 witness 独立重放等价性。
