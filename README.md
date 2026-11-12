# Range放大拒绝边界

下载网关允许多区间读取，攻击者可用大量重叠区间放大返回数据。给Go服务实现范围规划包，解析字节闭区间、开放尾部和后缀区间，依据固定对象长度规范化；合并读取但保留响应区间语义，限制最终响应字节及段数，预算计算包含边界头开销。用小对象全枚举核对不可满足范围、重复段和整数溢出。

## 实现

包 `netsec`（`example.org/netsec/q0129`）只依赖标准库。

- `range.go`：`Parse` 解析 `Range` 头，支持闭区间 `start-end`、开放尾部
  `start-`、后缀 `-suffix`；`Spec.Normalize(size)` 按固定对象长度规范化，
  越界区间返回包装 `ErrUnsatisfiable` 的 `*UnsatisfiableError`（携带区间下标）。
- `planner.go`：`PlanRequest` 规范化全部区间，去重完全相同的响应段，
  把重叠/相邻的物理读取合并为最少的 `Reads`，但 `Segments` 仍按请求顺序
  保留各响应段语义；`Limits` 限制段数、负载字节、整包字节和读取次数。
- 预算：`MultipartEstimator` 按 `multipart/byteranges` 实际线格式计算
  每段边界头与结束边界，`WireBytes` 计入这些开销；也可传入自定义
  `Estimator`，传 `nil` 时只计负载。
- 所有累加经 `checkedAdd/checkedSub` 做 int64 溢出检查，溢出以
  `ErrOverflow` 失败而不是回绕。

```go
specs, err := netsec.Parse(r.Header.Get("Range"))
plan, err := netsec.PlanRequest(objectSize, specs, netsec.Limits{
    MaxSegments: 100,
    MaxDataBytes: 8 << 20,
    MaxWireBytes: 16 << 20,
}, netsec.NewMultipartEstimator())
```

`Plan` 同时给出 `DataBytes`、`ReadBytes`、`WireBytes`、`DuplicateParts`
与 `AmplificationBytes`（被重复请求的负载量），便于监控放大行为。

## 测试

- `range_test.go`、`planner_test.go`：解析、规范化、合并/去重、各限制、
  边界头预算与溢出。
- `exhaustive_test.go`：对长度 0–5 的小对象枚举所有区间形状，并用独立
  oracle 逐对（另对最小对象逐三元组）核对规范化、不可满足范围、重复段、
  合并读取与线格式预算；另含 int64 边界溢出枚举与 `FuzzParse`。

运行：`go test ./...`（`go test -fuzz=FuzzParse .` 可做持续模糊测试）。
