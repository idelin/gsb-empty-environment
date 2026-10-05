# unify — 类型合一求解器

支持函数类型、元组类型、柔性类型变量与显式刚性变量的合一（unification）。

## 特性

- **类型项**：`Var`（柔性变量）、`Rigid`（刚性变量，只能与同名刚性变量合一，
  绝不绑定到任意类型）、`Fun`（函数）、`Tuple`（元组）。
- **Occurs 检查**：绑定变量前沿结构做 BFS，拒绝 `α = (α -> β)` 之类的递归
  （无限）类型。
- **最短冲突方程链**：合一在 union-find 上进行，但每一次类合并都把用户写下
  的方程记入独立的 provenance 图（以成员节点为顶点）。冲突时对该图做 BFS，
  输出连接冲突双方取值来源的最短方程链。
- **路径压缩不毁解释**：`find` 只改写 parent 指针；provenance 图以节点身份为键
  单独存放，压缩后解释链完整如初（见 `TestPathCompressionPreservesProvenance`）。
- **最一般替换（MGU）**：`Solver.Subst()` 产出幂等替换——等价变量统一重命名为
  类代表元，值域不含定义域变量；`Subst.Compose` 组合后归一化，`s ∘ s = s`。
- **枚举验证**：`enum_test.go` 枚举深度 ≤ 2 的全部小类型树（约 1.9 万对），
  逐对验证：成功则两侧在 MGU 下相等、替换幂等且自组合不变、occurs 拒绝生效；
  对变量做一致重命名后结果与原结果同构。

## 使用

```go
s := unify.NewSolver()
if c := s.Unify(a, b); c != nil {
    fmt.Println(c) // 冲突原因 + 最短方程链
}
mgu := s.Subst() // 幂等的最一般替换
```

运行测试：`go test ./...`；示例：`go run ./cmd/udemo`。
