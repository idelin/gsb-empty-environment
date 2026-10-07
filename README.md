# dfeq — DFA 语言等价比较库

比较两个确定自动机（协议状态机）的接受语言，输入允许部分迁移函数与未展开的外部片段。

## 语义

- **拒绝汇点**：缺失的迁移隐式进入拒绝汇点（sink 自环、不接收）。
- **空串**：比较从 `(startA, startB)` 开始，空串 ε 首先被检查。
- **反例**：在乘积图上按 (长度, 字典序) 优先队列做 BFS，首个接收状态不一致的
  乘积节点给出**最短且字典序最小**的区分串，并输出乘积状态路径 `Result.Path`。
- **未知**：可达状态仍带未展开片段时返回 `Unknown` 与阻塞片段 ID，
  完整展开前绝不宣称等价。

## API 概览

- `Compare(a, b *DFA) Result` — 一次性比较（`Equivalent` / `Counterexample` / `Unknown`）。
- `Comparer` — 增量比较器：`ResolveFragment` 异步补齐片段后，下次 `Compare`
  从阻塞节点增量续探；结果携带绑定的 DFA 版本号。
- `Query`（`NewQuery`）— 快照当前版本，旧查询永远绑定旧版本。
- `VerifyCounterexample(a, b, word)` — 验证反例只被一方接受且最短字典序最小。
- `CrossCheck(a, b, maxLen)` — 短字串枚举的独立暴力核对。
- `ValidateResult(a, b, cached)` — 用全量乘积图重跑核对缓存结果与反例最短性。

## 测试

```sh
go test -race .
```
