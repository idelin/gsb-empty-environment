# errser — 错误链 / 聚合错误的安全序列化

纯 Go 标准库实现。把错误图（cause 链、聚合、共享子图、循环）序列化为版本化 JSON
信封，并能安全地解码回来。

## 设计要点

- **稳定类型码与显示文本分离**：`Kind`（如 `validation`、`timeout`）是稳定的机器可
  读码，`msg` 是可自由变更的显示文本。`errors.Is` 按 Kind 匹配，跨序列化保持语义。
- **敏感字段白名单**：上下文字段仅当 key 在 `Encoder.Allow` 中才输出；非白名单字段
  整体丢弃（key 与值都不可见）。非标量值替换为 `<pkg.Type>` 占位符，编码过程不调用
  `String()` 等任何宿主方法。
- **非泄漏规则（已声明并被测试固定）**：两份仅在敏感值上不同的输入，产生**字节一致**
  的公开输出——结构与所有计数（节点数、ctx 条目数）均相同。见 `TestNonLeakage`。
- **身份保持**：共享原因只序列化一次，后续出现为 `{"ref":N}` 回引用；解码后共享节点
  是同一指针。循环边标记 `"cycle":true`，解码恢复为真实指针环，`Error()` 与再序列化
  均有深度上限，不会无限递归。
- **预算与截断**：`Limits` 限制深度 / 节点数 / 消息长度 / ctx 条数 / 输入字节数。
  非严格模式超预算时发出 `{"kind":"truncated","trunc":"depth|nodes"}` 截断标记；
  严格模式（`Strict`）返回 `ErrBudget`。
- **解码安全**：
  - 先验证输入字节数，再解析；先验证节点数 / 字符串长度 / 边索引，再按声明量分配。
  - Kind 走封闭 `switch` 分发；未知类型一律解码为通用码 `opaque`，保留显示文本。
  - 无反射、无按名构造，载荷无法实例化任意宿主类或触发宿主代码执行。

## 支持的序列化范围（声明）

| 输入 | 序列化结果 |
|---|---|
| `*errser.Error` | kind + msg + 白名单标量 ctx + 单个 cause |
| `*errser.Aggregate` | msg + 有序子错误列表 |
| 其它（外部）error | `opaque` 叶节点，msg 截断；沿 `Unwrap()` 跟随，受深度预算约束 |
| ctx 值 | 仅 nil/bool/string/数字保留；其余变 `<pkg.Type>` 占位符 |

## 信封格式

```json
{"v":1,"root":0,"nodes":[
  {"id":0,"kind":"validation","msg":"...","ctx":{"field":"email"},"cause":1},
  {"id":1,"kind":"timeout","msg":"..."},
  {"id":2,"ref":1},                
  {"id":3,"ref":0,"cycle":true},   
  {"id":4,"kind":"truncated","trunc":"depth"}
]}
```

节点三种形态：真实节点（`kind`）、回引用（`ref`，`cycle` 表示指向祖先）、
截断标记（`kind:"truncated"`）。边（`cause`/`children`）只指向更大的 id，
`ref` 只指向更小的 id，解码器据此在分配前完成全部校验。

## 运行样例

```
$ go run ./cmd/demo
=== serialized envelope ===
{ "v": 1, "root": 0, "nodes": [
    { "id": 0, "kind": "aggregate", "msg": "system report", "children": [1, 6] },
    { "id": 2, "kind": "validation", "msg": "create user failed",
      "ctx": {"field": "email"}, "cause": 3 },
    { "id": 3, "kind": "timeout", "msg": "db dial timeout", "ctx": {"host": "db.internal"} },
    { "id": 5, "ref": 3 },                      <- 共享原因回引用
    { "id": 8, "ref": 6, "cycle": true }        <- 循环标记
    ... ] }
=== decoded Error() (cycle-safe) ===
system report (2 error(s))
  0: batch import failed (2 error(s)) ... [timeout] db dial timeout
  1: [internal] worker crashed: [internal] supervisor restart: ...  …   <- 深度截断，终止
=== shared cause identity preserved: true ===
=== non-leakage (sensitive-only diff => identical bytes): true ===
{"v":1,"root":0,"nodes":[{"id":0,"kind":"auth","msg":"login failed","ctx":{"user":"alice"}}]}
=== unknown kind decodes to generic code: "[opaque] rm -rf /" ===
```

注意输出中不含 `password` / `token` 等敏感字段；`os/exec.Command` 这类伪装 kind
被解码为通用 `opaque`，不会实例化任何宿主类型。

## 测试结果

```
$ go vet ./... && go test -v ./...
--- PASS: TestRoundTripChain                 链式往返 + errors.Is 跨序列化
--- PASS: TestSharedCauseIdentityPreserved   菱形共享原因解码后指针同一
--- PASS: TestCycleTerminatesAndRecovers     循环终止、cycle 标记、解码恢复环
--- PASS: TestNonLeakage                     仅敏感值不同 => 字节一致、计数一致
--- PASS: TestWhitelistAndValueSanitization  白名单过滤 + 非标量占位符
--- PASS: TestUnknownKindDecodesToOpaque     未知类型保留通用码，不执行宿主代码
--- PASS: TestForeignErrorEncodesAsOpaque    外部 error => opaque
--- PASS: TestBudgetStrictFails              严格模式预算失败返回 ErrBudget
--- PASS: TestBudgetTruncationMarkers        非严格模式截断标记可往返
--- PASS: TestMessageTruncation              消息截断带省略标记
--- PASS: TestExhaustiveSmallGraphs          3 节点全部 64 种 cause 图穷举：
                                             终止、可解码、再序列化字节稳定
--- PASS: TestMutation                       截断/位翻转/恶意 kind 变异：
                                             不 panic、不执行宿主代码、被接受者仍可稳定往返
--- PASS: TestLengthValidatedBeforeAllocation 超长输入/节点洪泛/越界边/自引用均拒绝
--- PASS: TestAggregateRoundTrip             聚合往返
PASS
ok  	errser
```
