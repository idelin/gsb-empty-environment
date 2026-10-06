# layermerge

Go 虚拟层合并库：按顺序叠加内存中的目录树，输出带来源标注的最终树。

## 合并语义

- **顺序叠加**：`Merge(l0, l1, ...)` 中 `l0` 为最底层，上层逐个覆盖。
- **白化项**：层内名为 `.wh.<name>` 的条目删除所有下层同名节点（含子树），自身不出现在结果中。
- **不透明目录**：目录内含 `.wh..wh..opq` 标记时，屏蔽该目录所有下层子项，仅保留本层子项；普通目录则与下层合并。
- **类型替换**：上层节点与下层同名节点类型不同时整体替换（文件↔目录双向）；目录↔目录默认合并。
- **硬链接**：`Hardlink(name, target)` 仅在**本层**树内解析（建层时绑定 `Resolved`），目标只存在于下层时建层报错，绝不跨层解析。
- **来源标注**：合并后每个节点的 `Layer` 字段记录引入它的层序号；合并目录保留其首次出现的层号，子项各自标注。

## 逐层展开核对

`Expand(layers...)` 返回每应用一层后的累计快照 `[]*Snapshot`，可逐步核对白化与类型替换的中间状态；`Merge` 即取最后一个快照。

## 示例

```go
layers := []*layermerge.Layer{
    layermerge.MustLayer("base",
        layermerge.File("app.conf", "v1"),
        layermerge.Dir("data", layermerge.File("old.db", "x")),
    ),
    layermerge.MustLayer("patch",
        layermerge.Dir("data", layermerge.Opaque(), layermerge.File("new.db", "y")),
    ),
    layermerge.MustLayer("top",
        layermerge.Hardlink("db.link", "/data/new.db"),
    ),
}
root, _ := layermerge.Merge(layers...)
fmt.Print(root.Dump()) // 每个节点带 (kind) 与 [layer N] 标注
```

运行演示：`go run ./cmd/overlaydemo`；测试：`go test ./...`。
