# PCF 帧读取器行为说明

PCF（Prefix-Coded Frames）是一个带独立校验的前缀编码帧归档格式及其流式读取器。
设计目标：归档中一帧坏掉后，后续合法帧仍应可读；读取器只报告**字节范围**级别
的结论，绝不凭压缩字节猜测不存在的"精确原始损坏位置"。

## 帧格式

所有整数小端序。一帧占 `HeaderSize + PayloadLen + CRCSize` 字节：

| 偏移 | 字段        | 大小   | 说明                                   |
|------|-------------|--------|----------------------------------------|
| 0    | Magic       | 4      | 固定 `"PCF1"`                          |
| 4    | Seq         | 4      | 序号（读取器原样透传，不做连续性假设） |
| 8    | RawLen      | 4      | 原始（解压后）载荷字节数               |
| 12   | PayloadLen  | 4      | 压缩载荷字节数                         |
| 16   | Table       | 256    | 每个符号的前缀码长度（0=未用，≤15）    |
| 272  | Payload     | 变长   | 规范霍夫曼位流，MSB 优先，末字节零填充 |
| …    | CRC32       | 4      | IEEE，覆盖此前全部帧字节               |

结构合法性检查（任一失败即"帧头非法"）：

- 魔数匹配；
- `RawLen ≤ MaxRawLen`、`PayloadLen ≤ MaxPayloadLen`（默认各 1 MiB，限制单帧尺寸）；
- `RawLen == 0` 当且仅当 `PayloadLen == 0` 且码表全零；
- 码表：长度 ≤ 15、至少一个符号、Kraft 和 ≤ 1（不过订阅）、单符号码长必为 1；
- 位计数自洽：`RawLen ≤ PayloadLen*8 ≤ RawLen*15 + 7`。

载荷级校验（失败即"坏帧"，但帧头已被确认）：

- CRC32 必须匹配；
- 载荷必须恰好解出 `RawLen` 个符号，剩余填充位 < 8 且全为 0。

## 读取状态机

读取器维护一个未分类字节缓冲区、绝对偏移和模式位，分两类模式：

**正常模式**（位于已确认的帧边界上）：

- 逐帧解析。结构合法且 CRC、载荷校验全部通过 → 产出 `Frame` 事件；
- 帧头结构合法但 CRC/载荷校验失败 → **完整消费已声明的帧长度**，把这一段
  产出为 `Bad` 事件。载荷中出现的魔数字节**不会**被当作帧头；
- 帧头或长度非法 → 进入重同步。

**重同步模式**：

- 按字节偏移扫描候选：每个位置先查魔数，再校验结构，再校验 CRC 与载荷，
  **只有结构及 CRC 全部有效才接受**为帧；接受前的所有字节并入 `Bad` 范围；
- 结构合法但 CRC 失败的候选**不被接受**，只前进 1 字节继续扫描；
- 找到合法帧后回到正常模式。

**暂停与恢复**：`ScanBudget` 限制单次 `Feed`/`Close` 调用内的重同步扫描步数
（0 = 不限）。预算耗尽时读取器暂停，缓冲区、偏移、模式、未发出的坏字节范围
全部保留；再次调用 `Feed(nil)` 即续扫，不漏字节。正常模式下消费已确认帧长度
不消耗预算（帧尺寸本身已被上限约束）。等待更多输入而暂停时同样保留全部状态。

**收尾（Close）**：流结束时仍无法确定的剩余字节（如截断的帧尾）产出 `Gap`
事件；末尾确定不是帧的垃圾字节产出 `Bad` 事件。

## 事件语义

`Feed`/`Close` 返回的事件流满足：

- 按流偏移升序、互不重叠、无缝拼接——每个输入字节恰好被归类一次，
  后续完整帧只读一次；
- `Frame`：已解码帧，`Data` 为原始载荷，`Seq` 为序号；
- `Bad`：确认不可解码的字节范围，`Data` 为原始流字节，`Reason` 说明拒绝原因
  （如 `CRC mismatch`、`oversubscribed prefix code`、`bad magic`）；
- `Gap`：无法确定的字节范围（截断尾部），`Data` 为原始流字节。

**重要保证**：CRC 失败只意味着"这段字节不是发送方写下的那一帧"。读取器
**不会**把 CRC 失败解释成某一个原始字符必错，不输出部分解码结果，不猜测
损坏点——损坏定位精度就是字节范围。

## 限制

- 重同步中若候选帧头结构合法但载荷尚未收齐，读取器会等待输入凑齐声明的
  长度（受单帧尺寸上限约束，内存有界）；若流在此结束，余下字节报 `Gap`。
  读取器不会对未收齐的候选做投机性跳过。
- 攻击者若能构造 CRC32 碰撞可伪造帧；本格式面向意外损坏恢复，不防篡改。

## 损坏构造器（测试与 CLI 共用）

- `FlipBit(b, off, bit)`：单比特翻转；
- `InsertFakeMagic(b, off, filler...)`：插入伪魔数；
- `InsertTruncatedHead(b, off, seq, data, headLen)`：插入截断头；
- `CorruptCodeTable(b, frameOff)`：写入过订阅的错误码表。

## CLI

```sh
go build -o pcf ./cmd/pcf

pcf encode  -frame 65536            < raw    > stream      # 切帧编码
pcf decode  -budget 0 -chunk 65536  < stream > raw         # 解码，事件报告到 stderr
pcf corrupt -mode flip      -offset 9000  -bit 3   < stream > out
pcf corrupt -mode fakemagic -offset 12000 -len 40  < stream > out
pcf corrupt -mode truncate  -offset 8000  -len 200 < stream > out   # 删除字节
pcf corrupt -mode trunchead -offset 15000 -len 150 < stream > out   # 插入截断头
pcf corrupt -mode badtable  -offset 3348           < stream > out   # offset=帧起点
```

`decode` 把完好帧的载荷拼到 stdout，把 `frame`/`bad`/`gap` 事件报告到 stderr；
出现任何 `bad`/`gap` 时退出码为 1，否则为 0。

## 测试

`go test ./...` 覆盖：往返一致性、任意分块喂入等价性、载荷内魔数、帧间伪魔数、
插入截断头、错误码表、载荷单比特翻转、长度字段翻转、扫描预算暂停/恢复不漏字节、
截断尾部报 Gap、末尾垃圾报 Bad、完整帧无需 Close 即产出。所有用例均校验事件流
对输入区间的无缝无重叠覆盖，以及错误范围覆盖实际破坏字节。
