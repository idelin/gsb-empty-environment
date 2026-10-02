# promlite — 受限 PromQL 风格表达式求值器

纯 Go 标准库实现，无第三方依赖、不连接任何监控平台、不做展示看板。
遥测事件在本地确定性合成（`synth`），写入 JSON 夹具后可原样回放（`run`），
对每次求值输出**每一步实际消费的原始样本时间范围**。

## 受限表达式语法

```
expr     := add
add      := mul (('+'|'-') matchMod? mul)*
mul      := postfix (('*'|'/') matchMod? postfix)*
matchMod := 'on' '(' labels ')' | 'ignoring' '(' labels ')'
postfix  := primary ('offset' DURATION)?
primary  := NUMBER
          | selector
          | 'rate' '(' selector '[' DURATION ']' ')'
          | 'sum' 'by' '(' labels ')' '(' expr ')'
          | '(' expr ')'
selector := IDENT ('{' IDENT '=' STRING (',' IDENT '=' STRING)* '}')?
```

刻意不支持：直方图函数、子查询、`group_left/group_right`、比较/集合运算、正则匹配器。

## 语义规则（每条都有对应测试）

- **rate**：取左开右闭窗口 `(t-window, t]` 内的样本，按
  `(末值-首值+计数器重置修正)/(末时间-首时间)` 计算每秒增速；
  窗口内少于 2 个样本时该系列**不产生结果**（绝不凭空造数）。
- **sum by**：按给定标签分组求和，结果只保留分组标签。
- **offset**：把子表达式的求值时刻整体回移，不同 offset 读取各自对应的
  历史窗口（见 `TestOffsetReadsHistoricalWindow`）。
- **二元运算**：标量可与向量混合；向量间默认按**全部标签**一对一匹配，
  也可用 `on(...)` / `ignoring(...)` 显式指定匹配标签。
- **缺失系列不当 0 除数**：右向量中找不到匹配标签集时，该元素直接从结果
  中丢弃，绝不补 0（因此永远不会变成 `x/0`）；而真实存在的 0 值除数按
  IEEE 语义得到 `+Inf`，两者严格区分
  （`TestBinaryMissingSeriesNeverZeroDivisor` vs `TestBinaryRealZeroDivisorIsInf`）。
- **多对多拒绝**：任一一侧同一匹配签名出现多条系列时，返回明确的
  `many-to-many matching rejected` 错误，**拒绝执行而非笛卡尔扩张**
  （`TestManyToManyRejectedNotCartesian`）。
- **每步依赖样本范围**：求值返回一棵 trace 树，每个节点记录该步骤实际
  读取的原始样本时间区间 `[start .. end]` 与结果元素数。

## 夹具格式

```json
{"events": [{"metric": "http_requests_total", "labels": {"job": "api"}, "ts": "2026-10-02T03:30:00Z", "value": 1000}]}
```

## 运行样例

```console
$ go run ./cmd/promlite synth -out fixtures/demo.json
wrote 105 events to fixtures/demo.json

$ go run ./cmd/promlite run -fixture fixtures/demo.json \
    -query 'sum by (job) (rate(http_requests_total[2m])) / on(job) machine_cores'
query:   sum by (job) (rate(http_requests_total[2m0s])) / on(job) machine_cores
eval at: 2026-10-02T03:40:00Z

result:
  {job="api"} => 0.33333333333333337
  {job="web"} => 0.16666666666666666

dependency trace (per-step consumed sample ranges):
binary / on(job) | samples: [2026-10-02T03:38:30Z .. 2026-10-02T03:40:00Z] -> 2 element(s)
├─ sum by (job) | samples: [2026-10-02T03:38:30Z .. 2026-10-02T03:40:00Z] -> 2 element(s)
│  └─ rate window=2m0s (t-window, t] | samples: [2026-10-02T03:38:30Z .. 2026-10-02T03:40:00Z] -> 3 element(s)
└─ selector machine_cores | samples: [2026-10-02T03:40:00Z .. 2026-10-02T03:40:00Z] -> 2 element(s)
```

offset 读取历史窗口（求值时刻 03:40，实际消费 03:33:30–03:35:00 的样本）：

```console
$ go run ./cmd/promlite run -fixture fixtures/demo.json \
    -query 'rate(http_requests_total{job="api"}[2m]) offset 5m'
result:
  {instance="a", job="api"} => 1
  {instance="b", job="api"} => 1.1666666666666667

dependency trace (per-step consumed sample ranges):
offset 5m0s (evaluated at 2026-10-02T03:35:00Z) | samples: [2026-10-02T03:33:30Z .. 2026-10-02T03:35:00Z] -> 2 element(s)
└─ rate window=2m0s (t-window, t] | samples: [2026-10-02T03:33:30Z .. 2026-10-02T03:35:00Z] -> 2 element(s)
```

非法多对多匹配被明确拒绝（`http_requests_total` 每个 job 有两条实例系列，
而 `machine_cores` 每个 job 只有一条，未聚合直接 `on(job)` 相除）：

```console
$ go run ./cmd/promlite run -fixture fixtures/demo.json \
    -query 'http_requests_total / on(job) machine_cores'
error: evaluation error: many-to-many matching rejected: label set {job="api"} matches multiple series on the left side of "/"
```

## 测试结果

```console
$ go test ./...
ok  	promlite	0.331s
?   	promlite/cmd/promlite	[no test files]
```

共 17 个测试全部通过，覆盖：选择器瞬时取值、rate 窗口/计数器重置/样本不足、
offset 历史窗、sum by 分组、缺失系列丢弃、真实 0 除数、on/ignoring 匹配、
多对多拒绝、标量混合运算、每步依赖范围、夹具回放一致性、解析器正误用例。

## 代码结构

- `model.go` — 样本/标签模型与 `Store`（瞬时点查、窗口查询）
- `parser.go` — 受限语法词法/递归下降解析与规范化打印
- `eval.go` — 求值器：rate/sum by/offset/二元运算、匹配规则、trace 树
- `fixture.go` — JSON 夹具读写（回放）
- `synth.go` — 确定性本地遥测合成器
- `cmd/promlite/main.go` — `synth` / `run` 命令行
