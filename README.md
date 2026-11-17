# Range放大拒绝边界

下载网关允许多区间读取，攻击者可用大量重叠区间放大返回数据。给Go服务实现范围规划包，解析字节闭区间、开放尾部和后缀区间，依据固定对象长度规范化；合并读取但保留响应区间语义，限制最终响应字节及段数，预算计算包含边界头开销。用小对象全枚举核对不可满足范围、重复段和整数溢出。

## rangeplan 包

实现位于 `rangeplan/`，按“解析 → 规范化 → 规划/限额”分层：

- `Parse`：严格解析 `Range: bytes=` 头，支持闭区间 `N-M`、开放尾部 `N-`、
  后缀 `-N`，拒绝语法错误与超出 int64 的数字。
- `Normalize` / `NormalizeAll`：依据固定对象长度把区间裁剪为闭区间
  `[Start, End]`；越界区间返回 `ErrUnsatisfiable`（对应 HTTP 416），
  超出对象尾部的区间按 RFC 7233 裁剪而非报错，输入顺序与重复区间保留。
- `PlanRange`：产出 `Plan`。`Plan.Reads` 为合并重叠/相邻区间后的上游读取
  跨度；`Plan.Segments` 保留每个请求区间的响应语义（重复区间仍各自成段）。
- 限额：`Limits.MaxRanges` 限制原始区间数，`MaxSegments` 限制响应段数，
  `MaxResponseBytes` 限制完整响应大小。
- 预算：`Plan.TotalBytes = DataBytes + OverheadBytes`，开销逐段计算
  multipart 边界行、`Content-Type`、`Content-Range` 头、空行及段尾 CRLF，
  并包含结束边界；所有累加经 `add64` 溢出检查，溢出返回 `ErrOverflow`。
- `HTTPStatus` 把包内错误映射为网关应返回的状态码（400/416/500）。

## 验证

`rangeplan/exhaustive_test.go` 对长度 0–6 的小对象全枚举所有区间形态及其
两两、三三组合，逐项核对：

- 不可满足区间的丢弃与整体 416；
- 合并读取与请求并集逐字节一致、无重复读取；
- 重复区间保留为独立响应段但只产生一次上游读取；
- 独立拼装 multipart 信封，其精确长度等于 `TotalBytes`；
- int64 边界下的后缀/闭区间裁剪与累计溢出。

运行：`cd rangeplan && go test ./...`（当前语句覆盖率 92.9%）。
