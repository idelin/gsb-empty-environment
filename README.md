# 格式字符串参数消费

日志安全审查需要发现不可信格式串和参数类型错位。实现供Go审查宿主调用的格式分析包，格式支持显式位置、顺序位置、动态宽度与精度、整数与字符串转换，禁止混用两种位置模式；每次消费记录来源参数及类型约束，动态宽度也占参数。对受污格式返回无法静态保证。用独立解释器验证重复位置和截断转换位置。

## 入口

- `Analyze(format string, tainted bool) *Result`：主入口。`tainted=true`
  表示格式串来自不可信数据源，结果仍尽力解析但 `Guaranteed=false`，并带
  `tainted-format` 诊断。
- `AnalyzeFormat(format string) *Result`：可信字面量的便捷入口。
- `Interpret(format string) (*Interpretation, error)`：独立状态机解释器，
  与主分析器不共享解析代码，用于交叉验证重复位置与截断转换位置。

## 结果与语义

- `Result.Consumptions` 按出现次序记录每次参数消费：零基 `Index`、是否
  显式 `Explicit`、是否 `Repeated`、角色 `Role`（值/动态宽度/动态精度）、
  动词 `Verb`、类型约束 `Constraint`、是否截断 `Truncating`。
- 动态宽度 `*` 与动态精度 `.*` 各自占用一个 `KindInt` 参数，消费次序与
  Go `fmt` 一致（宽度 → 精度 → 值）。
- 显式位置 `%[n]`、`%[n]*`、`.[n]*` 与顺序位置禁止混用，混用产生
  `mixed-position-modes` 诊断并置 `MixedModes`。
- 同一参数位置出现互不兼容的约束时产生 `type-conflict`；宿主可用
  `TypeMismatch(actual []Kind)` 直接比对实参类型。
- `RepeatedPositions()` 给出被重复引用的显式位置；
  `TruncatingPositions()` 给出带精度截断的字符串转换位置。
- 悬空 `%`、未闭合 `[n]`、`%[3]2d`、`%[3].2d` 等非法结构产生
  `malformed-format`/`bad-arg-index`，并使 `Guaranteed=false`。

## 验证

`analyze_test.go` 与 `extra_test.go` 覆盖顺序/显式消费、动态宽度精度、
混用拒绝、重复位置、截断位置、类型冲突、受污格式与非法格式，并同时用
标准库 `fmt.Sprintf` 运行时行为和独立解释器 `Interpret` 双向交叉校验。
