# resolve

Go 名称解析库：输入已解析的声明与引用，输出每个引用的解析终点与解释链。

## 模型

- 声明按 `(module, name)` 定位（`Key`），分为真实定义（`Def`）与重导出（`ReExport`，指向另一个 `Key`）。
- 重导出可成链；模块还可以通过 `SetImport` 导入其他模块的名字。
- 同名本地声明遮蔽导入（解释链的 `Step.Note` 会标注 shadowing）。
- 解析结果 `Result.Status`：
  - `Resolved`：链终止于真实定义，`Endpoint` 为终点；
  - `Unresolved`：链终止于无声明、无导入的键；
  - `CycleUnresolved`：链成环且没有真实定义，`Cycle` 给出环上的键。
- `Result.Explain()` 输出人类可读的完整解释链。

## 两种解析器

- `Direct`：直接解析模型，每次从头解析，作为基准真值。
- `Resolver`：增量解析器，缓存结果并记录依赖；`SetLocal` / `DeleteLocal` /
  `SetImport` / `DeleteImport` 只失效受影响的缓存项。

`incremental_test.go` 中的随机测试对两种模型施加相同的增删改序列，
逐步断言二者对所有引用的解析结果完全一致。

## 示例

```go
r := resolve.NewResolver()
r.SetImport(resolve.Key{Module: "a", Name: "x"}, resolve.Key{Module: "b", Name: "x"})
r.SetLocal(resolve.Key{Module: "b", Name: "x"}, resolve.Decl{
    Kind:   resolve.ReExport,
    Target: resolve.Key{Module: "c", Name: "x"},
})
r.SetLocal(resolve.Key{Module: "c", Name: "x"}, resolve.Decl{Kind: resolve.Def})

res := r.Resolve(resolve.Key{Module: "a", Name: "x"})
fmt.Println(res.Explain())
// a.x (import) -> b.x (re-export) -> c.x (definition) => resolved at c.x
```

## 测试

```sh
go test ./...
```
