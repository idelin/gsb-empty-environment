# 文件系统块占用双证据

主机取证需要发现被目录隐藏或被多个对象非法共享的数据块。写供Go取证宿主调用的一致性包，输入目录引用图、对象区段表和分配位图，区段共享仅在同一受信共享组内合法；分别报告可达但未分配、已分配但无对象引用、对象存在但目录不可达及非法重叠，并保留最短引用路径。用小块设备逐块参考验证硬链接不误算两份对象、合法共享与对象号代次复用。

## 包结构（`package netsec`）

- `types.go`：证据模型。`ReferenceGraph`（目录引用图，根对象 + 目录项边）、
  `SegmentTable`/`Object`/`Extent`（对象区段表与受信共享组）、
  `AllocationBitmap` 接口与内存实现 `Bitmap`；对象身份为 `ObjectRef{ID, Generation}`。
- `finding.go`：发现类别与报告。四类核心发现常量
  （`KindReachableUnallocated`、`KindAllocatedUnreferenced`、
  `KindUnreachableObject`、`KindIllegalOverlap`）外加代次悬空
  `KindStaleEntry`；每条发现带 `Path []PathNode` 最短引用路径。
- `analyze.go`：`Analyze(graph, table, bitmap) (*Report, error)`。
  BFS 求最短可达路径，只沿代次匹配的目录项扩展；随后对区段表逐块独立记账。
- `device.go`：小块内存设备 `MemoryDevice`（数据 + 分配位 + 引用计数）与
  `VerifyBlockReferences`，用独立重放逻辑逐块交叉核对分析结论。

## 判定语义

- 共享合法当且仅当同一块的全部持有者属于同一个**非空**共享组；
  私有对象（空组）之间、跨组之间的重叠一律报 `KindIllegalOverlap`。
- 记账单位是对象而非目录项：硬链接的多个目录项指向同一对象，
  其区段只计一次，不会被算成两份对象。
- 目录项代次与区段表当前对象不符（含对象号已删除或被新一代次复用）
  报 `KindStaleEntry`，且不参与可达性扩展。
- `KindReachableUnallocated`：块被任一可达对象引用但位图未分配；
  `KindAllocatedUnreferenced`：位图已分配但无任何区段引用；
  `KindUnreachableObject`：对象在区段表中但无当前代次目录路径。

## 宿主调用示例

```go
report, err := netsec.Analyze(graph, table, bitmap)
if err != nil { /* ErrNilInput / ErrDuplicateObject / ErrInvalidExtent */ }
for _, finding := range report.Findings {
    log.Printf("%s block=%d path=%s detail=%s",
        finding.Kind, finding.Block,
        netsec.PathString(finding.ShortestPath()), finding.Detail)
}
```

## 验证

`go test ./...` 使用 8/12 块的小设备逐块交叉验证：
硬链接引用计数为 1 而非 2、同组共享合法、跨组/私有重叠非法、
对象号代次复用下旧代次引用悬空而新代次仍可达，以及四类发现与
`VerifyBlockReferences` 的独立重放结果逐块一致。
