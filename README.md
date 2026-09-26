# histmerge

合并边界不同的**累计直方图**（cumulative histogram），仅在可精确转换时合并。
纯 Go 标准库实现，无外部依赖。遥测事件在本地合成、由夹具（JSONL 文件）回放；
不接入任何监控平台，也不包含展示看板。

## 数据模型

`Histogram{Bounds, Counts}`：`Counts[i]` 是观测值 `<= Bounds[i]` 的累计次数。
校验规则（`Validate`）：

- 边界严格递增、有限（拒绝 NaN/Inf）；
- 累计计数非负且**单调不减**——计数下降即判为输入错误；
- 边界与计数长度一致。

## 合并规则（`Merge`）

1. **边界相同**：计数直接相加，逐项做 int64 溢出检查。
2. **一方边界是另一方的子集**：把较细的直方图投影（`Project`）到较粗的
   边界上——对累计直方图而言这是精确转换——再相加。结果采用较粗的边界。
3. **否则拒绝合并**，错误信息中输出双方的边界。

粗桶**绝不**被均分/拆成细桶，因为那样无法精确还原。合并前对两个输入
都做单调性校验；合并不修改输入。

## 遥测事件与回放

事件类型：`define` / `observe` / `merge` / `snapshot`。
`Replay` 按序处理事件，任何非法输入（未知直方图、非法边界、边界不兼容、
溢出）都会中止回放并指出事件序号。超出最大边界的观测值被丢弃并计数。
`SynthesizeEvents(seed, n)` 用固定种子在本地确定性地合成事件流。

## 布局

- `histogram.go` — 累计直方图、校验、投影、合并、溢出检查
- `telemetry.go` — 事件类型、JSONL 加载、回放、本地合成
- `cmd/histmerge/main.go` — 命令行：回放夹具或合成事件
- `fixtures/events.jsonl` — 正常回放样例（同边界相加 + 细→粗投影合并）
- `fixtures/incompatible.jsonl` — 边界互不包含、被拒绝的样例
- `histogram_test.go` / `telemetry_test.go` — `go test` 测试

## 运行样例

```
$ go run ./cmd/histmerge -fixture fixtures/events.jsonl
snapshot api_latency_us
  bounds: [0.1 0.5 1 5]
  counts: [0 2 4 5]
snapshot api_latency_us
  bounds: [0.1 0.5 1 5]
  counts: [1 4 6 8]
dropped 1 observation(s) above last bound of api_latency_fine

$ go run ./cmd/histmerge -fixture fixtures/incompatible.jsonl
replay rejected: event 4: merge "a" += "b": cannot merge: incompatible bounds
[0.5 1 5] vs [0.5 2 5]: neither is a subset of the other and coarse buckets
are never split
(exit status 1)

$ go run ./cmd/histmerge -gen 1000 -seed 7
synthesized 1004 events locally (seed 7)
snapshot latency_coarse
  bounds: [0.1 0.5 1 5]
  counts: [111 389 619 995]
dropped 2 observation(s) above last bound of latency_coarse
dropped 3 observation(s) above last bound of latency_fine
```

## 测试结果

```
$ go test ./...
ok  	histmerge
?   	histmerge/cmd/histmerge	[no test files]
```

覆盖：单调性校验（下降即输入错误）、非法边界、同边界相加、细→粗精确
投影合并（双向对称）、互不包含时拒绝并输出界限、int64 溢出检查、夹具
回放、合成事件的确定性与合并后有效性。
