# BGP FlowSpec规则的来源与更具体路由校验

攻击者通过邻居下发 FlowSpec 规则，把别人前缀的流量丢弃。为Go路由进程写可导入的 FlowSpec 验证包（RFC 8955 第 6 节）：规则仅在目的前缀的最佳单播路由来自同一邻居 AS，且前缀范围内没有来自其他邻居 AS 的更具体单播路由时才生效；NLRI 组件须按类型升序；验证失败不安装并产生告警。用他人前缀、更具体路由、乱序组件和合法规则验证。

## 实现

可导入包位于 `flowspec/`（导入路径 `example.org/netsec/q0716/flowspec`），仅依赖标准库。

- `flowspec/validate.go`：RFC 8955 §6 校验。规则只有在以下条件全部满足时才安装：
  - NLRI 组件类型严格升序、无重复；
  - 目的前缀存在覆盖它的最佳单播路由，且该最佳路由来自同一邻居 AS（iBGP/本地内部路由豁免）；
  - 目的前缀范围内不存在来自其他邻居 AS 的更具体单播路由（同邻居 AS 的更具体路由允许）。
  - 无目的前缀组件的规则只做结构校验（§6 仅约束目的前缀）。
- `flowspec/wire.go`：FlowSpec NLRI 线格式编解码（§4.2 组件头的 len-of-length 选择符、§4.2.2 前缀编码）。
- `flowspec/manager.go`：`Manager` 负责接收、校验、安装/撤销规则；失败时不安装并通过 `Alerter` 告警（自带 `LogAlerter`）。
- `flowspec/memory_rib.go`：`RIB` 接口的内存参考实现，提供最长前缀匹配与更具体路由查询。

校验失败以带 `FailureCode` 的 `*ValidationError` 返回，同时产生一条 `Alert`（`foreign_origin`、`more_specific_foreign_route`、`components_out_of_order`、`duplicate_component`、`no_best_route` 等）。

## 测试

`go test ./...` 覆盖题目要求的四类场景：

- 他人前缀（最佳路由来自别的邻居 AS）→ 拒绝并告警；
- 前缀范围内存在其他邻居 AS 的更具体路由 → 拒绝并告警；
- NLRI 组件乱序（含从线格式解码的乱序报文）→ 拒绝并告警；
- 合法规则（同邻居 AS、无外来更具体路由、组件升序）→ 安装；

另含同 AS 更具体路由放行、无覆盖路由、iBGP 内部路径、IPv6、无目的前缀规则、线格式往返与撤销等用例。
