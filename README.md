# attrgram — 带属性文法（Attribute Grammar）求值器

纯 Go 标准库实现的语法制导翻译内核：AST 生成后，把把每个 AST 节点上的
属性实例及其依赖关系注册进依赖图，拓扑排序（Kahn 算法）后按序求值，
并检测属性计算的循环依赖。

## 结构

- `ast.go` — AST 节点定义（Program / Decl / Print / 字面量 / Var / Binary），每个节点携带 `Pos`（行:列）。
- `eval.go` — 求值内核 `Engine`：属性实例 = `(节点, 属性名)`，注册依赖后拓扑求值；循环依赖返回 `*CycleError`，列出无法调度的属性。
- `grammar.go` — 属性文法接线：
  - **继承属性** `env`：符号表环境（名字 → {类型, 值}），自顶向下传递；语句间顺序穿线（第 i 条语句的 `env` 依赖第 i-1 条的 `env.out`）。
  - **综合属性** `typ` / `val`：表达式类型与计算结果，自底向上返回。
  - **综合属性** `env.out`：声明语句把新绑定并入环境后向上/向后传递。
  - 类型不匹配、未定义变量、重复声明、除零均以 `Diagnostic{Pos, Msg}` 精准报告 AST 节点位置。
- `cmd/demo` — 运行样例。
- `attrgram_test.go` — `go test` 测试。

## 运行

```sh
go test ./... -v     # 测试
go run ./cmd/demo    # 样例
```

样例输出：

```
== inferred types & values ==
1:1: a : int = 3
2:1: b : int = 6
4:1: bad : <error> = <nil>
== print output ==
9
== diagnostics ==
4:8: type mismatch: cannot apply "+" to int and bool
5:7: undefined variable "missing"
```
