WebAssembly 2.0 模块加载器的核心安全边界是单趟线性的结构化控制流栈类型校验算法（官方规范 Appendix Validation Algorithm）：当 `unreachable`、`br`、`br_table` 或 `return` 把当前控制帧置为多态不可达态（`unreachable = true`）后，校验器绝不能简单跳过该块剩余指令——后续代码仍必须通过严格的类型统一检验！例如 `unreachable; i32.const 1; i64.add` 在栈顶已有明确类型 `i32`，与 `i64.add` 期望的第二个操作数类型冲突，必须**拒绝**；而 `unreachable; i64.add` 在未知底栈上可合法统一为 `i64` 并压入结果 `i64`，必须**通过**；更棘手的是 `block (param i32) (result i64)`、`loop`（其分支标签类型是参数类型而非结果类型！）、`if` 缺省 `else`（要求块输入与输出类型签名完全一致！）以及 `br_table` 所有目标标签的分支参数类型必须与多态/已知操作数栈同时兼容！为 WebAssembly 运行时交付可导入的 Go Wasm 2.0 函数体字节码校验与控制流类型推导包。输入为函数签名表、模块类型签名表、局部变量表、全局变量表（含可变性）及指令序列（支持多值 `block`/`loop`/`if`/`else`/`end`、`br`/`br_if`/`br_table`/`return`/`call`/`call_indirect`、`select` 无类型与带类型形式、`drop`、`local.*`/`global.*`、数值与转换指令及 `unreachable`）。规则：1）严格实现规范的双栈校验器（操作数类型栈含未知多态占位符 `Unknown` + 控制帧栈含 `opcode`/`start_types`/`end_types`/`height`/`unreachable`）；2）精确处理 `select` 无类型形式在多态栈上的双向类型约束（校验两操作数类型一致且仅限数值/向量类型，或同为 `Unknown` 时保持 `Unknown`！）；3）完整校验 `br_table` 全部目标标签（含默认标签）具有相同 `label_types` 长度与兼容类型、越界标签深度报错、`loop` 分支标签取 `start_types` 而 `block`/`if` 取 `end_types`；4）校验函数结尾栈高度精确等于函数返回类型，任何多压栈、少压栈、非法 `else` 或不可变全局写入均给出精确指令下标与错误分类。验收覆盖多态不可达栈后缀冲突拒绝 vs 合法统一通过、`loop` 与 `block` 标签类型反转、缺 `else` 的多值 `if`、无类型 `select` 多态约束以及 `br_table` 多标签类型一致与越界检查。

## wasmvalidate 包

`wasmvalidate` 是可导入的 Go 包，实现 WebAssembly 2.0 单趟线性函数体校验
（官方规范 Appendix Validation Algorithm 的双栈算法）。

- 操作数类型栈：具体值类型 + 多态占位符 `Unknown`（在不可达控制帧高度处弹栈时产生）。
- 控制帧栈：每帧记录 `opcode`、`start_types`、`end_types`、`height`、`unreachable`。
- `unreachable`/`br`/`br_table`/`return` 之后不跳过任何指令，后续代码仍在多态栈上严格统一类型。
- `loop` 分支标签取 `start_types`，`block`/`if` 取 `end_types`；缺省 `else` 的 `if`
  要求块输入与输出类型序列完全一致。
- `br_table` 校验全部目标（含默认标签）等长且类型逐一相同，并检查越界深度。
- 无类型 `select` 要求两个数值/向量操作数类型一致；双 `Unknown` 保持 `Unknown`。
- 所有错误为 `*ValidationError`，含稳定的 `ErrorKind` 分类与出错指令下标（整体错误为 -1）。

入口：`wasmvalidate.Validate(mod *Module, body *Body) error`。

```go
err := wasmvalidate.Validate(module, functionBody)
var ve *wasmvalidate.ValidationError
if errors.As(err, &ve) {
    // ve.Kind 为错误分类，ve.InstrIndex 为指令下标，ve.Message 为细节
}
```

运行测试：`go test ./...`
