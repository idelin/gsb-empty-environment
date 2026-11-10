# 离线固件补丁检查器

纯 Go、无外部依赖的离线检查器：给定原镜像大小、原镜像中的可信区间，以及由
`COPY 原始区` / `COPY 已生成区` / `ADD` 组成的补丁，判断受保护输出区的任一字节
是否来源于**不可信原区**或**新增数据（ADD）**，并返回首处问题字节的完整回溯链。

## 设计要点

- 不展开大镜像：输出区用一组有序的来源段（`segment`）表示，段数量与补丁操作数同阶，
  内存占用为 O(操作数)，与镜像大小无关。
- 历史不可变：每次覆盖只追加新层，并把被覆盖部分记为带 `deadOp` 的死亡历史层。
  `COPY 已生成区` 按操作发生时刻的快照（`bornOp < asOf <= deadOp`）解析来源，
  因此自重叠 `memmove` 不会成环，回溯链长度有限。
- 前向引用判定：`COPY 已生成区` 的源区间必须完全落在当前已写出的输出内，
  引用未生成位置返回 `ErrIllegalForwardReference`；允许源与目标在已生成区内重叠。
- 长度安全：所有 `src+len`、`dst+len`、输出增长都经 `uint64` 溢出检查，
  溢出统一返回 `ErrLengthOverflow`。
- 零长操作：不产生字节，直接跳过（包括跳过前向引用与越界校验）。

## 操作语义

| 操作 | 字段 | 含义 |
| --- | --- | --- |
| `OpCopyOriginal` | `Src, Dst, Len` | 从原镜像 `[Src,Src+Len)` 复制到输出 `[Dst,Dst+Len)` |
| `OpCopyGenerated` | `Src, Dst, Len` | 从当前已生成输出 `[Src,Src+Len)` 复制到 `[Dst,Dst+Len)`，允许重叠 |
| `OpAdd` | `Data`（`Len=len(Data)`） | 在输出尾部追加新增数据 |

## 快速上手

```go
checker, _ := NewChecker(origSize, []Range{{Start: 0, End: 4096}})
err := checker.ApplyPatch(ops)
if errors.Is(err, ErrIllegalForwardReference) {
    // 补丁非法：引用了尚未生成的输出
}
finding, err := checker.Check([]Range{{Start: 0, End: 8192}})
if finding != nil {
    // finding.Offset 是首个不可信字节，finding.Hops 是从输出到叶子来源的完整链
}
```

补丁可用 `EncodePatch` / `DecodePatch` 序列化为自包含的紧凑二进制格式（见 `patch.go`）。

## 错误哨兵

- `ErrLengthOverflow`：长度算术溢出。
- `ErrIllegalForwardReference`：COPY 已生成区引用未写出位置。
- `ErrOutOfBounds`：来源或目标越过其边界 / 出现空洞写出。
- `ErrMalformedPatch`：补丁结构或 ADD 长度与数据不一致。
- `ErrProtectedRangeOutOfBounds`：受保护区间超出已生成输出。
