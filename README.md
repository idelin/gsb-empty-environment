# 字节序混用数据流

本包为 Go 侧协议分析提供一个很小的、可解释的 IR，以及基于标签格的字节序传播。它解决的核心问题是：把网络序长度当成宿主序偏移参与范围计算时，分析器不能因为某一条路径“看起来安全”就选中该路径；合流必须保留冲突，直到代码显式转换字节序。

## 标签格

| 标签 | 含义 |
| --- | --- |
| `TagRaw` | 原始字节序列，尚未建立整数解释 |
| `TagBE` | 按大端/网络序解释的整数 |
| `TagLE` | 按小端/宿主序解释的整数 |
| `TagConst` | 与字节序无关的常数 |
| `TagConflict` | 合流后仍存在无法判定的字节序冲突 |
| `TagUnknown` | 没有可用信息 |

`Join` 的关键规则：

- `BE ⊔ LE = Conflict`，不会选择任意一端。
- `Conflict ⊔ X = Conflict`（`Unknown` 除外），冲突不会被另一条已知分支悄悄覆盖。
- `Const` 是中性元，`Const ⊔ BE = BE`。
- 只有 `OpConvert`（显式大小端转换）能把 `Conflict` 归一为 `BE` 或 `LE`；`OpSwap` 只交换字节，不能消除合流冲突。

## IR

`Program` 是一段带隐式参数的线性 SSA IR，寄存器 `0..len(Params)-1` 是输入。指令包括：

- 字节读取：`OpReadByte`、`OpReadBE`、`OpReadLE`（带宽度）。
- 组装：`OpPack`，按 `OrderBig`/`OrderLittle` 把 lane 组装成整数。
- 交换字节序：`OpSwap`；显式归一：`OpConvert`。
- 算术：`OpAdd`、`OpSub`、`OpMul`、`OpShl`、`OpShr`、`OpAnd`、`OpOr`、`OpXor`。
- 范围运算：`OpSlice`，语义为 `base[offset:offset+length]`。
- 合流：`OpPhi`，对多个到达值做 `Join`。
- 宿主调用：`OpHost`，用 `ResultTag` 声明调用效果；Go 前端可把 `binary.BigEndian.Uint32` 之类的宿主调用降为专用读指令或 `OpHost`。

`Validate` 会检查寄存器唯一性、操作数先定义、宽度和操作数个数。

## 危险范围报告

`Analyze` 对每个 `OpSlice` 检查：

- 网络序长度与宿主序偏移混用；
- 合流冲突未经显式转换就进入范围运算；
- 原始字节仍被当作 offset/length；
- 范围算术溢出或负范围；
- 已知 buffer 长度下的静态越界。

每条 `Finding` 都带 `base`、`offset`、`length` 三棵来源树；合流的两个分支都会保留在树里。`Report.String()` 可直接输出全部危险范围。

示例输出（节选）：

```text
dangerous range operation at wire.go:14:7: mixed-byte-order, out-of-bounds
  - mixed-byte-order: network-order (be) length is paired with a host-order (le) offset
  - out-of-bounds: range [20,4116) exceeds buffer length 64
  offset [le, known=20]:
    v4 = add(v2, v3) [le]
    |-- v2 = param host_off [le]
    `-- v3 = const 4 [const]
  length [be, known=4096]:
    v1 = param net_len [be]
```

## 使用

```go
p := &netsec.Program{
    Params: []netsec.Param{
        {Name: "payload", Tag: netsec.TagRaw, HasBytes: true, Bytes: 64},
        {Name: "net_len", Tag: netsec.TagBE, HasConst: true, Const: 4096},
        {Name: "host_off", Tag: netsec.TagLE, HasConst: true, Const: 16},
    },
    Instrs: []netsec.Instr{
        {Op: netsec.OpConst, Dst: 3, Imm: 4},
        {Op: netsec.OpAdd, Dst: 4, Args: []int{2, 3}},
        {Op: netsec.OpSlice, Dst: 5, Args: []int{0, 4, 1}},
    },
}
report, err := netsec.Analyze(p)
```

## 具体解释器

`Interpret` 在同一份直线 IR 上执行真实字节缓冲，支持读取、打包、换序、显式转换、算术和切片。测试覆盖：

- 正字节序样本：`00 00 01 00` 按 BE 读取为 `256`；
- 反字节序样本：同一字节按 LE 读取为 `65536`，再 `swap32` 回到 `256`；
- 安全切片：offset/length 都是 BE，范围落在 buffer 内；
- 危险切片：BE 长度与 LE offset 混用，运行时越界，抽象分析同时报告 `mixed-byte-order`。

```sh
go test ./...
go test -run 'ByteOrder|Vulnerable|SafeSlice' -v
```
