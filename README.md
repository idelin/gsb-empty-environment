# resolver

Go 名称解析分析库：输入已解析的声明（`Decl`）与引用（`Ref`），输出每个引用的解析终点与解释链。

## 模型

- 声明按 `(模块, 名称)` 定位（`Key`），分为三类：
  - `Definition`：真实定义；
  - `Reexport`：重导出另一模块的符号，可成链；
  - `Import`：从另一模块引入符号。
- 同一 `(模块, 名称)` 下，本地声明（定义或重导出）遮蔽同名导入。
- 沿重导出/导入链解析，遇到真实定义即终点；链成环且无真实定义时报
  `cyclic unresolved`；找不到声明时报 `unresolved`。
- `Result.Chain` 记录每一跳的解释（本地定义 / 重导出 / 导入 / 遮蔽 / 环）。

## 解析器

- `Direct`：全量直接解析，作为核对的基准模型。
- `Incremental`：缓存每个引用的结果及其依赖的声明键；`PutDecl` /
  `DeleteDecl` 只失效受影响的缓存，下次 `Resolve` 惰性重算。

## 测试

`go test ./...` 包含单元用例与随机化测试：随机增删改声明、增删引用，
每轮将增量结果与 `Direct` 全量重算逐引用比对（`reflect.DeepEqual`）。
可用 `RESOLVER_SEED` 更换随机种子。
