# paradl — 并行分段下载库（本地模拟）

在本地模拟带实体版本（ETag 风格）的并行分段（Range）下载，核心保证：

- **响应自描述**：每个响应携带实体版本 `Version`、实际覆盖范围 `Range`、
  数据与校验和 `Checksum`（sha256）。实际范围可能小于请求范围（短写）。
- **确认门槛**：一个范围只有在**完整落盘**（`WriteAt` 全部写入）并
  **回读校验通过**后才会被确认（记入确认集合并保存断点）。
- **重叠合并**：响应实际范围可以超出请求范围。与已确认范围重叠时，
  重叠字节必须**逐字节一致**才允许合并，否则报错。
- **版本失效**：任何响应的版本与本轮版本不一致（无论是新内容还是迟到
  的旧响应），本轮全部确认作废，重新 `Head` 后以新版本开新一轮。
- **原子断点**：断点先写临时文件、`fsync`、再 `rename` 覆盖，崩溃只会
  留下旧断点或新断点，不会留下半个文件。损坏的断点直接丢弃重下。
- **重启核验**：恢复时不盲信断点——逐条重读目标文件并重算校验和，
  验证失败的已确认块会被丢弃并重新下载；版本或大小变化则全部作废。

## 布局

- `range.go` — 范围与已确认集合（有序、去重、缺口计算）
- `source.go` — `Source` 接口与内存模拟源 `LocalSource`（确定性内容）
- `faults.go` — 故障文件定义（JSON）
- `checkpoint.go` — 断点的原子保存与加载
- `downloader.go` — 下载器主流程（轮次、工作池、确认、失效）
- `cmd/dldemo` — 命令行演示
- `examples/faults.json` — 故障文件示例

## 故障文件

```json
{
  "short_writes": [{"on_request": 3, "keep_bytes": 500}],
  "stale_responses": [{"on_request": 2, "delay_requests": 6}],
  "version_changes": [{"on_request": 5, "new_seed": 777, "new_version": "v1"}],
  "crash_on_checkpoint_save": 4
}
```

- `short_writes`：第 N 个请求只返回前 `keep_bytes` 字节（短写），
  下载器只确认实际到达的范围并补抓剩余部分。
- `stale_responses`：第 N 个请求时快照当前内容，延迟若干请求后把
  这份旧响应插回应答流（旧响应晚到），会因版本不符被拒绝。
- `version_changes`：第 N 个请求前改写源内容并提升实体版本，
  本轮全部确认失效。
- `crash_on_checkpoint_save`：第 N 次断点保存时以 `ErrSimulatedCrash`
  崩溃（断点切点），用于验证原子保存与恢复。

## 运行

```sh
go test ./...
go run ./cmd/dldemo -faults examples/faults.json -dst /tmp/out.bin -ckpt /tmp/out.ckpt.json
# 输出 CRASH 后去掉 crash_on_checkpoint_save 再跑一次即断点续传完成
```
