# 敏感数据区间组合

取证平台需要在不改变证据字节偏移的情况下向不同角色隐藏内容。实现供Go读取服务调用的区间遮罩包，输入不可变原文、重叠的敏感区间及标签，授权标签不足时逐字节替换为固定遮罩；区间更新创建新版本，ReaderAt固定版本，支持巨大稀疏区间。返回被遮罩范围证明，用逐字节参考核对边界相交、多标签交叠与并发旧版本读取。

## 实现

包 `netsec`（module `example.org/netsec/uid1278`）提供不改变字节偏移的区间遮罩：

- `NewDocument(origin, ...Option)`：保存不可变证据原文，`WithMaskByte` 自定义固定遮罩字节（默认 `'*'`），`WithLogicalLength` 声明超过物理后备的稀疏逻辑长度。
- `Range{Start, End, Label}`：半开区间 `[Start, End)`，区间可重叠、标签可不同；同一字节只要存在任一未授权标签即被遮罩。
- `Document.AddRanges` / `Document.ReplaceRanges`：每次更新发布一个不可变新版本，旧版本永久保留；`Document.Version(n)` / `Latest()` 取快照。
- `Version.Reader(labels...)`：返回固定在该版本与授权标签集合上的 `io.ReaderAt`，后续更新对其不可见；`ReadAt` 原位把无权字节替换为固定遮罩字节，偏移永不移动。
- `Reader.Proof()` / `MaskedRanges()`：返回最小、有序、互不相交的半开被遮罩范围证明（不泄露具体标签）。

内部把区间编译成只覆盖敏感字节的有序 `segment` 列表，稀疏区间的内存占用与覆盖量成正比，与区间之间的空隙大小无关；偏移统一使用 `int64`。`Document` 用读写锁保护版本发布，`Version`/`Reader` 不可变，全部方法可并发使用。

## 示例

```go
doc := netsec.NewDocument([]byte("0123456789"))
v, _ := doc.AddRanges(
    netsec.Range{Start: 2, End: 6, Label: "pii"},
    netsec.Range{Start: 4, End: 9, Label: "secret"},
)

// 无授权：两个区间的并集全部遮罩。
r := v.Reader()
buf := make([]byte, 10)
_, _ = r.ReadAt(buf, 0) // "01*******9"

// 仅持有 pii：仍受 secret 保护的 [4,9) 被遮罩（5 个字节）。
partial := v.Reader("pii")
_, _ = partial.ReadAt(buf, 0) // "0123*****9"
partial.Proof().Masked         // [{Start:4 End:9}]

// 新更新不影响固定在旧版本的 reader。
_, _ = doc.ReplaceRanges(netsec.Range{Start: 0, End: 1, Label: "x"})
_, _ = r.ReadAt(buf, 0) // 仍是 "01*******9"
```

## 测试

`go test -race ./...` 覆盖：

- 400 组随机重叠区间 × 全部 32 种标签子集，用独立的逐字节参考实现核对整段、单字节、随机窗口 `ReadAt` 与遮罩证明。
- 边界相交（相切、半开边界、跨边界窗口）、多标签交叠的联合授权语义。
- 版本固定读取与 8 个 goroutine 并发更新/读取的旧版本稳定性（`-race`）。
- 1 TiB 逻辑长度的巨大稀疏区间（仅 4 KiB 物理后备）、空洞零字节读与 EOF 语义。
- 非法区间拒绝、防御性拷贝与证据不可变性。
