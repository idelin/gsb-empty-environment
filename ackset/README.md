# ackset — 批量确认序号集合编解码

`ackset` 把离散序号（`uint32`，模 2^32，支持回绕）集合编码为**连续区间
(range)** 与**有限位图 (bitmap)** 的段序列。编码器在给定格式下输出字节数
最小的编码；解码器拒绝重叠段与一切非规范形式。典型用途：批量确认
(ACK) 场景下对序号集合的紧凑传输。

## 线格式

整数均为大端；varint 为无符号 LEB128（最短形式）。

```
空集:    [0x00]
非空集:  [0x01] seg_count(varint >= 1) seg_1 ... seg_k

段:
  range:  [0x00] start(uint32) count(varint >= 1)
          覆盖 start .. start+count-1 (mod 2^32)
  bitmap: [0x01] start(uint32) nbits(varint, 2..MaxBitmapBits) data(ceil(nbits/8) 字节)
          第 i 位 (字节 i/8 的 bit i%8，LSB 优先) 置位 <=> start+i ∈ S
```

## 规范性与解码拒绝规则

`Decode` 依次做结构校验与重编码比对，拒绝：

- 段重叠或乱序（模 2^32 距离为 0 或 ≥ 2^31）→ `ErrOverlap`
- 总跨度 ≥ `MaxWindow` (2^31，序号空间的一半) → `ErrTooSparse`
- 空 range、bitmap 长度越界、首/末位为 0、填充位非零、全 1 位图
  → `ErrNotCanonical`
- 截断、未知模式/段类型、varint 溢出 → `ErrTruncated` / `ErrMalformed`
- 尾部多余字节 → `ErrTrailing`
- **非字节最小**：解码后重新编码，要求与输入逐字节相同。这把"可合并的
  相邻段""次优分段""超长 varint"等一切非规范形式统一收敛为
  `ErrNotCanonical`，无需枚举规则。

相邻段（两段之间无缺失序号）只有在合并不会更小时才合法（例如两个相邻
bitmap 合并后会超出 `MaxBitmapBits`），是否合法由重编码比对裁决。

## 编码器最小性

1. **锚点选择**：在循环序号空间的最大间隙处切开，使跨度最小。可编码集合
   的最大间隙必然 > 2^31，远大于一个段头（6 字节量级），故跨越最大间隙
   的位图永远不如切开，最大间隙切分是最优的。
2. **分段**：对切开的线性偏移序列做动态规划，候选为 range（限连续段）
   与 bitmap（窗口 ≤ `MaxBitmapBits`），代价为精确字节数；按
   (载荷字节, 段数) 字典序比较即可同时最小化 `seg_count` varint 长度。
3. 段间空隙在格式中零开销（各段携带绝对 `start`），故 DP 互相独立。

`TestExhaustiveMinimal` 对 10 元素窗口的全部 2^10 个子集，用暴力枚举
（所有循环切分 × 所有分段）交叉验证编码器输出确为字节最小。

## 回绕前后的分段规则

序号按模 2^32 处理：集合先旋转到最大间隙之后，再以相对偏移分段，因此
回绕点（0 与 2^31 处）两侧的分段规则完全一致。`TestWrapIsomorphism`
对同一掩码在普通窗口与跨回绕窗口下逐位对照：编码长度相同、解码元素
仅差固定位移。`TestExhaustiveRoundtrip` 覆盖 16 元素窗口全部 2^16 个
子集在四种基准（普通、空间中点、2^31 边界、跨 0 回绕）下的往返。

## 限制

- 单集合元素数 ≤ `MaxElements` (2^20)
- 单个 bitmap 段 ≤ `MaxBitmapBits` (8192) 位
- 集合最小覆盖窗口 < 2^31

## 运行测试 cases

```sh
go test ./...
```
