# viewbuilder

增量视图构建器：从固定快照扫描，重放扫描窗口内的变更，达到切换序号后原子替换旧视图。

## 构建流程（`Builder.Rebuild`）

1. **快照扫描**：`Source.Snapshot()` 返回固定序号 `snapSeq` 的一致性快照，据此构建新视图。
2. **变更重放**：切换序号 `switchSeq = Source.LatestSeq()`（扫描结束后取）。`Poll` 拉取
   `snapSeq` 之后的变更；投递可能乱序，`reorderBuffer` 缓存乱序事件，只释放从
   `next` 开始的**连续前缀**；遇到空洞就重新拉取补齐，删除事件一律保留、按序应用。
3. **原子切换**：视图水位达到 `switchSeq` 后，`atomic.Value` 一次性替换服务视图；
   旧视图不被修改，持有旧指针的读者仍看到一致状态。
4. **切换回执**：向源端发送 `{generation, switchSeq}` 回执；回执丢失时重试，源端按
   generation 幂等去重，重试安全。

构建在任意阶段失败（扫描/重放/切换）都会中止并保持旧视图服务，重试从快照重新开始，
天然幂等。`CatchUp` 用同一套重放机制把当前视图追平到最新序号。

## 测试（`builder_test.go`）

- 每次公开查询（`Get`/`Len`/`Keys`/`Range`）都与 `Source.RecomputeAt(view.Seq())`
  的**全量重算**结果对拍。
- 故障注入：扫描/重放/切换失败（`Faults`）、乱序投递（`SetShuffle`）、
  切换回执丢失（`SetDropAck`）。
- 随机化测试混合变更、乱序、故障注入与 Rebuild/CatchUp，每轮对拍。

运行：`go test -race ./...`
