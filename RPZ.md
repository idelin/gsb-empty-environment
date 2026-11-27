# RPZ 策略区编译与裁决引擎（Go 包 `rpz`）

`rpz` 是一个与传输层无关的 Response Policy Zone 引擎：它消费递归解析器归一化后的观测
（客户端地址、QNAME 与整条 CNAME 链、应答地址、权威 NS 主机），输出策略裁决，便于接入
任意 DNS 解析服务。仅依赖 Go 标准库。

## 能力

- 五类触发器：`RPZ-CLIENT-IP`、`QNAME`（含 CNAME 链逐跳）、`RPZ-NSDNAME`、`RPZ-NSIP`、`RPZ-IP`。
- 七类动作：`NXDOMAIN`（`CNAME .`）、`NODATA`（`CNAME *.`）、`PASSTHRU`（`CNAME rpz-passthru.`）、
  `DROP`（`CNAME rpz-drop.`）、`TCP-ONLY`（`CNAME rpz-tcp-only.`）、本地数据替换（A/AAAA/TXT/CNAME）、
  递归通配符（`*.bad.example` 只匹配子域，不匹配裸域）。
- 反向 CIDR owner 编解码：IPv4 反序十进制（`24.0.1.168.192.rpz-ip` → `192.168.1.0/24`），
  IPv6 反序 nibble 与单次 `zz` 零段压缩（`48.zz.db8.2001.rpz-ip` → `2001:db8::/48`）。
- 精确决胜序：策略区按配置顺序（高优先决）；同区固定 `CLIENT-IP > QNAME > NSDNAME > NSIP > IP`；
  同触发器按最长 CIDR 掩码、精确名优先于通配符决胜。
- `PASSTHRU` 短路：高优区命中即放行，停止求值所有低优区。
- CNAME 链逐跳 QNAME 审查，以及 `min-ns-dots` 对根/顶级域权威的保护。
- BIND 风格 zone 文本解析：`$ORIGIN`/`$TTL`、括号续行、相对 owner、通配 owner。

## 包结构

- `rpz/ipcodec.go`：反向 CIDR owner 编解码（IPv4/IPv6、`zz` 压缩）。
- `rpz/trie.go`：二进制 bit-trie，最长前缀匹配。
- `rpz/name.go`：域名标签、通配与子域工具。
- `rpz/zone.go`：策略区编译器、规则合并、name/IP 索引。
- `rpz/parse.go`：zone 文本解析器。
- `rpz/engine.go`：多区/多触发器裁决引擎与请求/裁决类型。

## 用法

解析 zone 文本：

```go
zone, err := rpz.ParseZone("policy.example.", strings.NewReader(zoneText))
engine := rpz.NewEngine([]*rpz.Zone{zone})
verdict := engine.Decide(rpz.Request{QName: "malware.example."})
if verdict.Hit {
    // verdict.Rule.Action: NXDOMAIN / NODATA / DROP / TCP-ONLY / PASSTHRU / LOCAL-DATA
}
```

或用编程式 API 构建策略区：

```go
zone := rpz.NewZone("policy.example.")
zone.AddRule("evil.example.policy.example.", rpz.Record{Type: "CNAME", Value: "."})
zone.AddRule("48.zz.db8.2001.rpz-ip.policy.example.",
    rpz.Record{Type: "CNAME", Value: "rpz-drop."})
```

`min-ns-dots` 通过选项调整（默认 2，保护根/顶级域权威；设 0 表示检查全部 NS 主机）：

```go
engine := rpz.NewEngine(zones, rpz.WithMinNSDots(2))
```

## 测试

```
go test ./...
go test -race ./...
```
