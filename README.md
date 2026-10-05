# pagecheck — 有序页文件检查器

Go 实现的有序页文件格式 + 独立检查器。每页头部摘要含最小键、最大键、
记录数、下一页 ID；记录按键非递减排列（重复键合法，按写入的明示次序保留）。

## 结构

- `pagefile/pagefile.go` — 页格式与编解码。帧界由 `RecordBytes` 驱动，
  `Count` 纯为可校验的摘要，因此"计数不符"不会破坏解析。
- `pagefile/checker.go` — 独立检查器 `Check(*File) *Report`，检测：
  页内乱序（`LocalOrder`）、最小/最大键与内容不符（`MinMismatch`/`MaxMismatch`）、
  计数不符（`CountMismatch`）、跨页逆序（`CrossPageInversion`）、
  循环链接（`Cycle`）、断链（`BrokenLink`）。
  `Report.BadPages` 为最小坏页集合：仅被至少一条违例直接牵连的页；
  跨页问题同时列出两端（任一侧都可能是错的），无辜邻页不入集。
- `pagefile/query.go` — 查询层 `Get`/`Scan`。任何查询先跑 `Check`，
  校验失败返回 `*ValidationError`（携带完整 `Report`），绝不默默跳过坏页。

## 测试

`pagefile/checker_test.go` 为每种元数据/内容不一致构造了样例，并验证：

- 含页内与跨页重复键的正常文件通过校验，且查询按明示次序返回重复记录；
- 单个坏页不牵连邻居（最小坏页集合）；
- 校验失败后 `Get`/`Scan` 拒绝服务并暴露坏页集合。

运行：

```sh
go test ./...
```
