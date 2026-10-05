# ACX1 自定义整数算术编码格式说明

本格式用于压缩**带上下文标签的二进制事件序列**。它是完全自定义的整数算术编码，
**不声称、也不追求与任何视频编码标准（H.264/H.265 CABAC 等）兼容**。
仅使用 Go 标准库实现。

## 1. 事件与上下文模型

- 事件 = `(label, bit)`：`label` 为 0..255 的上下文标签，`bit` ∈ {0,1}。
- 每个上下文独立维护一对计数 `(zero, one)`，初始均为 **1**（拉普拉斯平滑）。
- 编码一位后**只更新该位所属上下文**：
  - 对应计数加 1；
  - 若更新后 `zero + one == 4096`，两个计数**各自向上取半**（`(x+1)>>1`，对 x≥1 结果至少为 1）。
- 编码器与解码器在**完全相同的位置**（每编/解一位之后）执行同一更新规则，保证模型同步。
- 符号 0 的累积频率为 `c0 = zero`，总量 `total = zero + one`；符号 1 的累积频率下界为 `c0`。

## 2. 算术编码核心（Witten-Neal-Cleary 方案）

### 位宽与舍入

- 码值寄存器 `low / high / value` 为 **32 位**无符号整数（存于 `uint64`，恒 < 2³²）。
- 中间乘积 `rng * c0 ≤ 2³² × 4096 = 2⁴⁴`，用 64 位无符号整数计算，不会溢出。
- 区间划分采用**向下取整**整数除法：

  ```
  rng   = high - low + 1
  bound = rng * c0 / total        // floor
  bit=0 → high = low + bound - 1
  bit=1 → low  = low + bound
  ```

- 归一化不变式保证 `rng > 2³⁰ > total ≥ c0`，故 `1 ≤ bound ≤ rng-1`，子区间永不为空。

### 归一化（E1/E2/E3）与延迟进位

每次缩区后循环执行：

| 条件 | 动作 |
|---|---|
| `high < 2³⁰`（E1） | 输出位 0，随后输出全部延迟位（取反） |
| `low ≥ 2³¹`（E2） | 输出位 1，随后输出全部延迟位（取反）；`low, high -= 2³¹` |
| `2³⁰ ≤ low 且 high < 3×2³⁰`（E3） | 延迟计数 `follow++`；`low, high -= 2³⁰` |
| 否则 | 停止 |

每轮之后 `low <<= 1`，`high = high<<1 | 1`（解码器同时对 `value` 做同样移位并读入新位）。
E3 + bit-plus-follow 即**延迟进位**：当中点两侧的悬而未决区间最终坍缩时，
进位通过输出相反位序列传播，无需回溯已输出字节。

### 终止

```
follow++
if low < 2³⁰ { emit(0) } else { emit(1) }
```

保证最终码值（尾部补零）落在 `[low, high]` 内；输出流按 MSB-first 打包，末尾用 0 位补齐到字节。
解码器预填 32 位窗口，不足 4 字节时以 0 填充。

## 3. 帧布局

```
偏移  字段        类型        说明
0     magic       4 字节      固定 "ACX1"
4     count       uvarint     事件总数 N（解码器据此精确停止，不靠填充猜长度）
..    numCtx      uvarint     上下文个数，1..256
..    payloadLen  uvarint     算术编码载荷字节数
..    labels      N 字节      上下文标签序列：每事件 1 个原始字节，值必须 < numCtx
..    crc32       4 字节 LE   IEEE CRC-32，覆盖逐事件的 (label, bit) 两字节序列
..    payload     payloadLen  算术编码输出字节流
```

- 标签序列按**原始字节**存储（每事件 1 字节，不建模压缩）；其编码规则即"N 个标签字节依序排列"。
- 解码器解码恰好 N 个事件后停止，重算 CRC 并与帧内值比对。
- `payloadLen` 使解码器能精确区分载荷与截断/尾部垃圾。

## 4. 限制与错误处理

| 条件 | 错误 |
|---|---|
| 事件数 > `MaxEvents`（2²⁶） | `ErrTooMany`（长度超限） |
| 上下文个数不在 [1,256]，或帧声明数超过调用方上限 | `ErrNumContexts` / `ErrContextLimit` |
| 标签 ≥ numCtx | `ErrBadLabel`（非法上下文） |
| bit ∉ {0,1} | `ErrBadBit` |
| magic 不符 | `ErrBadMagic` |
| 头部/标签/CRC/载荷不足，或载荷字节数 ≠ payloadLen | `ErrTruncated` / `ErrTrailing` |
| CRC 校验失败 | `ErrChecksum` |

## 5. 暂停与恢复（快照）

`Encoder.Snapshot()` 序列化全部编码状态，`Resume()` 恢复后继续 `Put`/`Frame`，
输出与一次性编码**逐字节一致**（由 `TestResumeByteIdentical` 保证）。快照布局：

```
"ACXS"  4 字节
numCtx  uvarint
count   uvarint
crc     4 字节 LE
low     8 字节 LE
high    8 字节 LE
follow  8 字节 LE
cur     1 字节（半字节缓冲）
nbits   1 字节（0..7）
labels  uvarint 长度 + 原始字节
out     uvarint 长度 + 原始字节（已产出的载荷字节）
ctxs    numCtx × (zero u32 LE, one u32 LE)
```

恢复时校验：计数 ∈ [1, 4096]、`low ≤ high ≤ 2³²-1`、`nbits ≤ 7`、无尾随字节。

## 6. 正确性核对方法

- **有理数参考**（`reference_test.go`）：用 `math/big.Rat` 在精确实数区间 `[lo, hi)` 上
  按同一模型切分（无归一化、无舍入），对短序列验证：
  1. 整数编码器输出的码值落在精确区间内；
  2. 用精确有理解码器从该码值还原出的位序列与原序列完全一致。
- **长序列往返**：20 万事件、多上下文、多偏斜度的编码→解码逐事件比对。
- **真实压缩率**：测试日志报告实际载荷大小（如 p(1)=1/128 时约 0.067 比特/事件，
  熵约 0.066），不预设任何无法保证的固定压缩门槛。
