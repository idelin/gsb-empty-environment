# reasoner

可逆事实演绎推理与最小解释生成器（Go，仅标准库）。

## 功能

- **正向链推理**：基于霍恩子句（`Rule{Premises, Conclusion}`），从已知事实推导新结论，支持多前提规则与多级链式推导。
- **证明树**：每次推导记录支持该结论的规则，形成证明结构。
- **最小解释**：`Explain(f)` 返回最短事实证据链（按证明深度优先、节点数次之），可展开为有序步骤与基础事实集。
- **真值维护**：`Retract(f)` 撤销基础事实后级联清除失去全部支持的派生结论；仍有独立证据的结论保留。

## 结构

- `engine.go` — 推理引擎：`Assert` / `Retract` / `Known` / `Facts`
- `proof.go` — 证明树与最小解释：`Explain` / `Steps` / `BaseFacts` / `Depth`
- `engine_test.go` — `go test` 测试（含验收边界）
- `cmd/demo/main.go` — 运行样例

## 运行

```sh
go test ./... -v   # 测试
go run ./cmd/demo  # 样例
```

## 验收场景

已知 `A` 与 `A->B` 推导出 `B`；再有 `C` 与 `C->B` 后，撤销 `A`，`B` 因 `C` 保留；
再撤销 `C`，`B` 失去全部支持被安全清除。见 `TestIndependentSupportAndCascade`。
