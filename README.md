# overlay — Go 虚拟层合并库

overlayfs 风格的内存目录树合并：多层按顺序叠加，输出带来源标注的最终树。

## 合并语义

- **顺序叠加**：`Merge(l0, l1, ...)` 从低到高逐层合并，同路径上层优先。
- **白化项**：`Layer.Whiteout(path)` 删除下层同名节点（含整个目录），自身不进结果。
- **不透明目录**：`Layer.MarkOpaque(dir)` 屏蔽下层同名目录的全部子项，仅保留本层子项。
- **类型替换**：同路径类型不同（如文件 vs 目录）时，上层节点整体替换下层子树。
- **硬链接层内解析**：`Link`/`LinkInode` 仅接受本层 inode，跨层引用返回错误；
  合并后同层硬链接仍共享同一节点。

## 示例

```go
l0 := overlay.NewLayer()
l0.File("etc/app.conf", "v1")
l0.File("data/stale.txt", "x")

l1 := overlay.NewLayer()
l1.File("etc/app.conf", "v2")
l1.Whiteout("data/stale.txt")

merged := overlay.Merge(l0, l1)
fmt.Print(overlay.Render(merged))
```

输出（每节点标注来源层与 inode，硬链接显示共享关系）：

```
// (dir) [layer 1]
  data/ (dir) [layer 0]
  etc/ (dir) [layer 1]
    app.conf (file) [layer 1] inode=2 "v2"
```

## 测试

`overlay/merge_test.go` 采用逐层展开模型：每叠加一层即核对中间树，
分别验证白化删除、不透明屏蔽、类型替换与硬链接层内解析。

```sh
go test ./...
```
