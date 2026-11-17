# 安全断言插值摘要

安全测试得到一条不可达漏洞路径，需要提炼阻断相似路径的条件。实现供Go分析服务调用的有限布尔插值包，输入前后两组CNF及共享变量，若合取不可满足，返回仅使用共享变量且被前组蕴含、与后组矛盾的公式；最多十二个共享变量。独立枚举验证两项性质，输入可满足时返回见证，不伪造安全证明。

## 用法

包名 `netsec`，入口为 `Interpolate(a, b CNF, shared []int)`。

- 字面量采用 DIMACS 风格整数编码：变量 `v > 0` 的正文字为 `Lit(v)`，取反用 `-Lit(v)`；零字面量非法。
- `CNF` 是子句（文字的析取）的合取；空子句恒假，空 `CNF` 恒真。
- 当 `a ∧ b` 不可满足时，返回 `*Interpolant`（DNF），其文字只引用 `shared` 中的变量。
- 当 `a ∧ b` 可满足时，返回 `*Witness`（同时满足两组 CNF 的赋值），插值项为 `nil`，绝不产出伪造的安全证明。
- `shared` 长度上限为 `MaxShared = 12`，超限返回 `ErrTooManyShared`；结构非法返回 `ErrInvalidInput`。
- 出现在两组 CNF 中的变量必须显式列入 `shared`，否则视为 `ErrInvalidInput`。

## 独立性保证

- 构造器 `solveCNF`（`sat.go`）是基于 trail、带 unit propagation 的可变状态 DPLL。
- 验证器 `Verify`（`verify.go`）使用另一套不可变 map 赋值、逐分支拷贝、全量重扫子句的独立 DPLL（`indSolve`），与构造器不共享任何实现。
- 验证枚举全部 `2^k`（`k ≤ 12`，最多 4096）个共享变量赋值，独立检查：
  - `A |= I`（每个 A 可行赋值下 I 成立）；
  - `B |= ¬I`（每个 B 可行赋值下 I 不成立）；
  - 词汇封闭（插值项不含私有变量）；
  - 最强形式（I 恰为 A 可行区域上的最强 DNF）。
- 任一检查失败，`Interpolate` 返回错误而不是不可靠公式。

## 复杂度

共享变量数为 `k` 时，构造与验证各发起 `2^k` 次 SAT 调用；`k = 12` 满规模在 Apple M1 Pro 上约 11 ms/次。包无状态，`Interpolate` 可被多 goroutine 并发调用。
