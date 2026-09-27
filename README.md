# mtusim — MTU 分片 / 重分片 / 重组离散事件仿真

纯 Go 标准库实现，无任何真实网络流量。所有包、队列与链路都运行在
整数 tick 的离散事件模型上。

## 布局

- `frag.go` — 分片核心：`FragParams{HeaderLen, MTU, Align}`、`Fragmentize`、
  `FragmentCount`、独立覆盖验证器 `VerifyCoverage`。
- `optimize.go` — `Optimize` 在头长 × 对齐单位上联合搜索最少片数；
  `ExhaustiveMinFragments` / `ExhaustiveOptimalCount` 对小长度穷举全部
  合法布局，作为最优性的独立真值。
- `reasm.go` — `Reassembler`：识别重复片、重叠字节冲突，拒绝混合 epoch。
- `sim.go` — 整数 tick 离散事件仿真：`Sim`、`Link`（FIFO 队列 + 串行化 +
  传播时延 + MTU 丢弃）、`Source`（超时后按新路径 MTU 重规划、epoch 递增）、
  `Receiver`（新 epoch 重置重组上下文，旧 epoch 视为过期）、`MTUChangeEvent`。
- `cmd/demo` — 运行样例。

## 规则

- 每片线速尺寸 = 头长 + 负载；MTU < 头长直接失败（`ErrKindMTUBelowHeader`）。
- 非最后片负载必须是对齐单位的整数倍；最后片可不满对齐单位，且可用满
  未对齐的负载预算。最少片数 = 1 + ceil((total - maxPayload) / unit)。
- 负载长度、报文长度为负一律拒绝（`ErrKindNegativeLength`）。
- 对齐单位放不进一帧且报文超过一帧时，明确报不可行（`ErrKindInfeasible`）。
- 每次 MTU 更正后的重规划使 epoch 递增；片段 (PacketID, Epoch, Offset, Len)
  始终可追溯原始字节范围；重组端不混合 epoch（`ErrKindEpochMismatch`），
  接收方策略是新 epoch 重置、旧 epoch 丢弃。
- 重组端：完全相同的重传计为重复片；重叠区域字节不一致报
  `ErrKindOverlapConflict`；一致的相邻/重叠片合并；重组结果等于原报文。

## 运行

```sh
export GO111MODULE=on
go test ./...      # 21 个测试
go run ./cmd/demo  # 离散事件样例
```

## 样例输出（节选）

```
tick    0  src  S1   plan    pkt=1 epoch=1 len=300 -> 3 frag(s) (mtu=128 header=20 align=8)
tick    1  path mtu update: link L1 mtu 128 -> 68
tick    2  link L1   DROP    pkt=1 epoch=1 range=[104,208) wire=124 > mtu=68
tick   25  src  S1   timeout pkt=1 epoch=1: re-planning with path mtu=68
tick   25  src  S1   plan    pkt=1 epoch=2 len=300 -> 7 frag(s) (mtu=68 header=20 align=8)
tick   28  rx   R1   reset   pkt=1 epoch 1 -> 2 (mtu corrected, re-planned)
tick   40  rx   R1   done    pkt=1 epoch=2 reassembled 300 bytes
tick   61  src  S1   FAIL    pkt=3 len=100 mtu=24: aligned payload unit 0: ...
tick   71  src  S1   FAIL    pkt=4 len=40 mtu=10: mtu 10 below minimum header 20

pkt1 reassembled == original: true (300 bytes, 2 link drops)
pkt2 reassembled == original: true (60 bytes)
optimizer: len=300 mtu=128 header in [8,40] align in [1,16] -> header=8 align=1 fragments=3
coverage verifier on plan: <nil>
```

## 测试结果

```
ok  mtusim  0.692s   # 21/21 PASS，含小长度穷举 vs 优化器全量比对
```
