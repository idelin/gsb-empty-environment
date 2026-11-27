# flowspec — FlowSpec 解码、ROA 准入与冲突求解

可导入的、纯标准库 Go 包，为骨干网边界路由安全控制面宿主提供：

1. **二进制解码/编码**：BGP FlowSpec NLRI（RFC 8955 / RFC 8956，IPv4/IPv6），
   完整覆盖组件 Type 1–12、操作符字节与动作扩展团体属性。
2. **RPKI ROA 归属准入**：目的前缀必须存在发起 AS **精确匹配**的有效 ROA，且
   `MinLength <= prefixLen <= MaxLength`；拒绝未授权、AS 冒用与超宽前缀黑洞。
3. **RFC 8955 优先级与冲突求解**：确定性优先级排序、匹配集覆盖/相交判定、
   `traffic-rate=0` 遮蔽放行、跨租户黑洞与 `redirect-to-IP` 劫持/冲突检测，
   以及对具体报文的 `Resolve`。

仅依赖 Go 标准库，**不发起任何网络访问**。

导入路径：

```go
import flowspec "formal.local/w27-29e3a2d557/flowspec"
```

## 组件与操作符

| Type | 组件 | 编码 |
|----|------|------|
| 1 / 2 | 目的 / 源前缀 | `type(1) prefixLen(1) prefix-bytes(ceil(len/8))` |
| 3 | IP 协议 | 操作符/值列表 |
| 4 / 5 / 6 | 端口 / 目的端口 / 源端口 | 同上（Type 4 匹配源或目的） |
| 7 / 8 | ICMP Type / Code | 同上 |
| 9 | TCP Flags（位掩码） | 同上 |
| 10 | Packet Length | 同上 |
| 11 | DSCP | 数值比较 |
| 12 | Fragment（位掩码） | 同上 |

操作符字节：

```
 7    6   5   4      3    2     1..0
 E    A   N   m/=    >    <     Len
0x80 0x40 0x20 0x10  0x08 0x04  宽度码
```

`E`=end-of-list，`A`=AND，`N`=NOT，`m/=`=match/相等；宽度码
`0/1/2 -> 1/2/4` 字节，`3` 为保留（严格解码判为非法操作符）。

扩展团体属性：`traffic-rate=0x8006`（末 4 字节为 IEEE-754 速率，`0` 即丢弃/黑洞）、
`traffic-action=0x8007`、`redirect=0x8008`、`redirect-to-IP=0x010b`。

NLRI 支持两种喂入方式：`DecodeComponents`（裸组件流）与 `DecodeNLRI`
（`[uint16 长度][组件]` 帧），另有 `DecodeAuto` 与 `DecodeNLRISequence`。

## 安全流水线

```go
rep := flowspec.NewAuditor(roas).Audit(rules) // 准入 + 优先级 + 冲突
pkt := &flowspec.Packet{}; pkt.SetDstIP(net.ParseIP("198.51.100.200"))
winner, idx, verdict := flowspec.Resolve(pkt, rules)
```

准入结果：`Valid / Invalid / NotFound`（RFC 6811）与确定性 `AdmissionCode`
（`origin-as-not-authorized`、`prefix-longer-than-min-length`、
`prefix-exceeds-max-length`、`roa-not-found`、`roa-expired` …）。

冲突发现码（`Finding.Code`）：

- `cross-tenant-blackhole-hijack`：AS 未获 ROA 授权却下发 `rate=0` 黑洞。
- `unauthorized-blackhole` / `roa-origin-mismatch` / `prefix-too-wide` …
- `shadowed-permit` / `partially-shadowed-permit`：高优先级丢弃遮蔽低优先级放行。
- `equal-priority-policy-conflict`：同优先级矛盾动作。
- `redirect-target-conflict` / `cross-tenant-redirect-hijack`。

## 确定性测试矩阵

```sh
GOPROXY=off go test ./... -count=1
```

- `TestIllegalOperatorMatrix`：保留宽度码、无匹配位、值截断、缺少 E、未知类型、
  前缀超长（全部 `errors.Is` 到对应哨兵错误，操作符类同时满足 `ErrIllegalOperator`）。
- `TestAdmissionMatrix`：精确 ROA、maxLength 内、跨租户 AS 冒用、NotFound、
  超宽 `/23`、超长、显式 `minLen`、过期 ROA。
- `TestDeterministicSecurityMatrix`：跨租户黑洞+遮蔽、超宽黑洞、同优先级矛盾、
  TCP 标志位掩码遮蔽、`redirect-to-IP` 目标冲突、跨租户重定向劫持，以及协议不相交
  不误报等负向对照。
- 另有全组件十六进制向量、规范重编码逐字节一致、IPv6、帧编解码、端到端
  NLRI+路径属性→审计、优先级顺序无关性与 `Resolve` 动态命中测试。

## 设计说明

- 匹配集关系（`MatchRelation`）对前缀包含、等值集合、数值区间与位掩码包含给出
  可判定结论；无法静态判定时返回 `RelUnknown`，审计按“潜在重叠”保守告警（安全侧
  fail-closed），不会臆断为不相交。
- `ComparePriority` 按 RFC 8955 组件顺序做确定性比较：最早出现差异的受限组件更具体、
  前缀按最长长度，相同特异度回退到规范无符号字节序；动作不影响优先级。
- 所有时间判断均可注入 `Auditor.Now`；零值 `time.Time` 关闭有效期校验，保证矩阵确定。
