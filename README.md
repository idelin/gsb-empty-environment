# 状态覆盖证据防伪

安全测试插件上报协议覆盖时，不能仅声明访问过某个状态。写供Go测试宿主调用的覆盖核验包，每个状态转换必须附前状态摘要、实际输入摘要、输出和模型版本，宿主用确定性转换回调重放后才计入；断链保留待定，冲突隔离。用跨模型拼接、遗漏拒绝转换与重复回执测试覆盖统计不会被虚报，模型回调异常不算已覆盖。

## coververify 包

`coververify` 子包供 Go 测试宿主核验插件上报的状态转换覆盖，核心 API：

- `New(oracle OracleFunc) *Verifier`：注入宿主的确定性转换回调；
- `RegisterRoot(modelVersion, state)`：登记每个模型版本的可信根状态，链只能从根续接；
- `RequireEdge(edge, expectReject)` / `RequireTransition(...)`：声明必须覆盖的转换边
  （拒绝转换需要显式声明拒绝预期）；
- `Submit(Receipt)`：回执四要素为前状态摘要 `PrevState`、实际输入摘要 `Input`、
  输出摘要 `Output` 和 `ModelVersion`，提交后全链重核；
- `Stats()` / `Snapshot()`：读取不会被虚报的覆盖统计与逐回执审计状态。

裁决语义：

- `covered`：前状态在同一模型版本的已核验链上，且 oracle 重放输出与回执一致；
- `pending`：断链，回执保留待定，前驱补齐后自动转为已覆盖；
- `conflict`：同一边出现互异输出，整边隔离、不计覆盖，下游回到待定；
- `mismatch`：重放输出与回执不一致；`errored`：回调报错或 panic，二者均不计覆盖，
  且失败不缓存，回调修复后重新提交即可恢复；
- 内容相同的重复回执按内容 UID 去重，不重复计边；跨模型拼接因版本隔离永远断链。

运行测试（含竞态检测）：`go test -race ./...`
