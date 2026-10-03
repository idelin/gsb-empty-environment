# CABAC 内核（Go 标准库实现）

仿视频编码标准（H.264/AVC 风格）的上下文自适应二进制算术编码内核：
为不同语法元素维护独立的上下文概率状态机，随每个 bin 的命中历史在
64 状态转移表上动态微调概率状态编号，榨干连续符号的统计相关冗余。

## 结构

- `cabac/` — CABAC 内核
  - `state.go` — 64 状态概率表（pLPS = 0.5·0.95^s，下限 0.01875）、
    H.264 风格 `transIdxLPS`/`transIdxMPS` 状态转移表、`Context`（自适应
    上下文）、`ContextSet`（按语法元素/ bin 位置索引的上下文池）、
    `StaticModel`（无上下文静态基线）
  - `encoder.go` / `decoder.go` — 32 位区间二进制算术编解码器
    （bit-plus-follow 进位控制，重归一化）
  - `bits.go` — MSB-first 位流读写
- `instr/` — 结构化指令流模型与对照实验（8 操作码小指令集，截断一元
  二值化，每个 bin 位置一个独立上下文）
- `cmd/cabacdemo/` — 运行样例

## 运行

```sh
go test ./...        # 全部测试
go run ./cmd/cabacdemo
```

## 运行样例（go run ./cmd/cabacdemo）

```
=== CABAC kernel demo: structured instruction stream ===

instructions: 50000   bins after unary binarization: 69609
opcode histogram: NOP=89.8% MOV=1.5% ADD=1.4% SUB=1.6% LOAD=1.5% STORE=1.5% JMP=1.3% CALL=1.5%

round-trip (CABAC adaptive):  OK
round-trip (static baseline): OK

model                                   bytes   bits/instr
raw fixed-length (3 bit/opcode)         18750        3.000
static context-free (global hist)        8702        1.392
CABAC (adaptive contexts)                3357        0.537

CABAC vs static model: 2.6x smaller
CABAC vs raw stream:   5.6x smaller

convergence of high-frequency instruction (NOP) cost:
NOP window     bits/NOP
2000           0.4145
4000           0.2505
...
22000          0.0940
...
42000          0.0305
44000          0.0505
```

高频指令（NOP）有效编码位长随编码推进从 ~0.41 比特收敛至 0.03–0.09
比特，稳定低于 0.2 比特的验收线；整体码率 2.6 倍优于无上下文静态模型、
5.6 倍优于定长原始流。

## 测试结果（go test ./...）

```
ok  example.com/cabac/cabac   — 8 个内核测试全部通过
ok  example.com/cabac/instr   — 5 个指令流实验测试全部通过
```

关键断言：

- `cabac.TestConstantSymbolCostConverges`：恒定符号稳态成本 0.0303
  bits/bin（< 0.2 验收线，深度收敛 < 0.05）
- `cabac.TestRoundTrip*`：1 ~ 100000 bins、多上下文共享、偏斜分布下
  编解码逐位一致
- `cabac.TestAdaptiveBeatsStatic`：局部结构化流上自适应内核
  1462B vs 静态模型 12473B
- `instr.TestNOPCostConverges`：高频指令成本末窗口 0.0375 bits（< 0.2）
- `instr.TestBeatsStaticModel`：CABAC 3636B vs 静态 9070B（2.49x）
