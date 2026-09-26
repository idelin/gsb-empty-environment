# histmerge

合并边界不同的**累计直方图**。只在可精确转换时合并，否则输出界限或拒绝。
纯 Go 标准库实现，无外部依赖；不接监控平台，不做展示看板。

## 语义

- 直方图为累计式：`counts[i]` 是 `value <= bounds[i]` 的观测数。
- `bounds` 严格递增；`counts` 单调不减、非负，否则报输入错误（`ErrCountDecrease` 等）。
- 合并规则：
  - 边界完全相同 → 计数直接相加（int64 溢出检查）。
  - 一方边界是另一方的子集 → 细直方图精确投影到粗边界后相加。
  - 否则拒绝（`ErrIncompatible`），可用 `Limits` 输出每个目标边界的 `[min, max]` 区间。
- **绝不**把粗桶凭空均分为细桶；`ConvertTo` 遇到目标边界缺失直接报错。
- 界限语义：对目标边界 `t`，真值介于「≤ t 的最大已知边界的累计计数」与
  「≥ t 的最小已知边界的累计计数」之间；两直方图的区间分别相加（带溢出检查）。

## 遥测事件

事件在本地确定性合成（`synth`，种子可复现），以 JSON Lines 存储，可由夹具回放
（`replay`）。回放按 metric 名折叠合并：可精确合并的累计，不可合并的拒绝并输出界限。

## 用法

```sh
go build -o histmerge .
./histmerge synth  [-n N] [-seed S] [-out file]   # 合成遥测事件（JSON lines）
./histmerge replay <fixture.jsonl>                # 回放并按 metric 合并
./histmerge merge  <a.json> <b.json>              # 精确合并两个直方图
./histmerge limits <a.json> <b.json>              # 输出合并结果的逐边界 [min,max]
```

## 运行样例

合成事件（确定性）：

```console
$ ./histmerge synth -n 3 -seed 1
{"name":"latency_ms","bounds":[5,10,20,50,100],"counts":[0,1,2,3,10]}
{"name":"latency_ms","bounds":[10,50,100],"counts":[0,8,16]}
{"name":"payload_bytes","bounds":[64,128,256,512],"counts":[6,15,19,26]}
```

子集边界精确合并（细 → 粗投影后相加）：

```console
$ ./histmerge merge fixtures/hist_fine.json fixtures/hist_coarse.json
{
  "bounds": [10, 50, 100],
  "counts": [13, 50, 75]
}
```

边界不兼容时输出界限（不虚构桶内分布）：

```console
$ ./histmerge limits fixtures/hist_a.json fixtures/hist_b.json
{
  "bounds": [64, 100, 128, 200, 256, 300, 512],
  "min": [2, 3, 7, 10, 13, 16, 19],
  "max": [3, 7, 10, 13, 16, 19, 19]
}
```

回放夹具（`latency_ms` 精确合并；`payload_bytes` 拒绝并给出界限，退出码 1）：

```console
$ ./histmerge replay fixtures/events.jsonl
metric latency_ms merged:
  <= 10: 13
  <= 50: 50
  <= 100: 75
metric payload_bytes not exactly mergeable; limits per bound:
  <= 64: [2, 3]
  <= 100: [3, 7]
  <= 128: [7, 10]
  <= 200: [10, 13]
  <= 256: [13, 16]
  <= 300: [16, 19]
  <= 512: [19, 19]
metric payload_bytes: boundaries incompatible: neither bound set is a subset of the other
error: 1 metric(s) could not be merged exactly
```

## 测试

```console
$ go test ./...
ok  	histmerge	0.094s
```

`go test -v` 共 21 个用例全部 PASS，覆盖：

- 单调性验证：累计计数下降 / 负计数 / 边界非递增 / NaN / 长度不符 → 输入错误
- 合并：同边界直接相加；子集边界双向精确转换；不兼容与部分重叠拒绝
- 溢出：计数相加与界限求和的 int64 溢出检查
- 拒绝粗桶细分：`ConvertTo` 目标边界缺失时报错
- 界限：区间正确性、边界对齐时退化为精确值、总数区间精确
- 遥测：合成确定性、JSON Lines 往返、夹具回放、坏事件不入合并结果
