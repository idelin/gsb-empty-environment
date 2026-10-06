# archivemerge

Go 归档合并库：接收按代际（generation）排序的清单（manifest），逐代折叠到一棵
虚拟树上，输出每个快照的可见树及每个节点的来源代际。

## 条目类型

- `KindFile` / `KindDir`：常规文件、目录条目。
- `KindFileTombstone` / `KindDirTombstone`：文件、目录墓碑。

## 合并语义

- 逐代顺序应用；每代输出一个 `Snapshot`，节点按路径排序，携带 `SourceGen`（来源代际）
  与 `Implicit`（是否为隐式祖先目录）。
- 文件条目替换路径上的整个子树（父类型变化 dir → file，旧后代被遮蔽）。
- 新条目需要下落时，路径上的文件被替换为隐式目录（父类型变化 file → dir）。
- 目录墓碑删除路径上的整个可见子树，遮蔽所有旧后代；但不遮蔽更新代际在同一路径下
  的显式重建项（局部重建）。
- 墓碑按类型匹配：文件墓碑只删文件，目录墓碑只删目录。
- 隐式目录被显式目录条目覆盖时提升为显式，来源代际更新。

## 用法

```go
snaps, err := archivemerge.Merge([]archivemerge.Manifest{
    {Generation: 1, Entries: []archivemerge.Entry{{Path: "/a/b.txt", Kind: archivemerge.KindFile}}},
    {Generation: 2, Entries: []archivemerge.Entry{{Path: "/a", Kind: archivemerge.KindDirTombstone}}},
})
```

## 验证

`merge_test.go` 用逐代虚拟树模型验证：父类型变化（双向）、祖先删除、
局部重建（显式与隐式）、墓碑类型匹配、来源代际追踪。

```sh
GO111MODULE=on GOCACHE=/tmp/gocache go test ./...
```
