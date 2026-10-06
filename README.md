# localdl — 并行分段下载库（本地模拟）

在本地模拟带版本的分段并行下载，覆盖确认、合并、失效与断点续传的完整语义。

## 核心语义

- **响应携带版本与范围**：每个响应带 `ETag`（实体版本）和实际返回范围
  （`Content-Range` 可大于请求范围），正文可能短于声明范围（短写）。
- **确认条件**：一个块只有在完整写入磁盘 **且** 摘要（manifest 中的
  SHA-256）校验通过后才被确认。
- **重叠合并**：响应与已确认块重叠时，重叠字节必须完全一致，否则返回
  `ErrInconsistentOverlap`（致命）；一致且完整覆盖未确认块时才合并确认。
- **版本失效**：响应版本与本轮不一致时向源站再验证——源站已是新版本则
  本轮全部失效（含已确认块与断点），重新开始；源站仍是本轮版本则该响应
  是迟到的旧响应，直接丢弃。
- **原子断点**：写临时文件 → fsync → rename → fsync 目录；重启后加载断点
  并逐块重新校验磁盘字节，校验失败的块被丢弃重下。

## 故障文件

故障通过 JSON 文件注入（`dl.LoadFaults`），请求计数从 1 开始：

```json
{
  "short_write_on_requests": [2],
  "expand_range_on_requests": [3],
  "corrupt_on_requests": [4],
  "version_change_after_requests": 2,
  "stale_replay_after_requests": 5,
  "crash_point": "before_rename",
  "crash_on_checkpoint": 2
}
```

- `short_write_on_requests`：正文截短但声明范围不变（短写）
- `expand_range_on_requests`：返回大于请求的范围（制造重叠）
- `corrupt_on_requests`：翻转首字节（制造不一致重叠）
- `version_change_after_requests`：服务 N 个请求后轮换实体版本
- `stale_replay_after_requests`：第 N 个请求附带捕获的旧版本响应（旧响应晚到）
- `crash_point` + `crash_on_checkpoint`：在第 N 次断点保存的
  `before_write` / `before_rename` / `after_rename` 切点模拟崩溃（`ErrCrash`），
  崩溃只触发一次，重建 `Downloader` 即模拟重启恢复

## 测试

```sh
go test ./dl/ -count=1 -race
```

覆盖：并行下载、短写重试、一致重叠合并、不一致重叠致命、版本变更整轮
失效、旧响应晚到丢弃、两个断点切点崩溃恢复、重启核验已确认块。
