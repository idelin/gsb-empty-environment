# splitter — 本地双目标分流器

业务记录软删除后路由到归档库、未删除路由到在线库。跨两个目标的迁移无法天然原子，
因此每个事件都先持久化**意图**（journal），再写目标，最后提交**版本指针**；
查询只通过指针访问**一个已确认位置**，另一侧的旧副本对读视图不可见，由 GC 回收。

## 布局

```
splitter/            核心库
  types.go           Event / Record / Pointer / 错误
  target.go          Target 接口、FileTarget（文件适配器）、FaultTarget（故障注入）
  journal.go         持久化意图与确认进度（intended -> target_written -> 删除）
  store.go           Apply / Recover / Get / Scan（指针视图）
  snapshot.go        持久化快照（GC 的引用来源之一）
  gc.go              只回收无指针且无快照引用的副本
  scan.go            ScanReference：全量扫描取每键最高版本的参考实现
  store_test.go      故障注入测试
cmd/splitter/        可重放 CLI
```

## 一致性规则

- 每个主键只接受**严格更高**的版本。
- 同版本同内容（含 `is_deleted` 相同）→ 幂等成功；同版本不同内容 → `ErrConflict`。
- 更低版本（延迟删除、乱序更正、重复投递）→ `ErrStale`，**绝不回滚指针**，
  因此延迟删除无法复活已恢复键的旧归档副本。
- `is_deleted=false` → online；`is_deleted=true` → archive。

## 崩溃恢复

`Apply` 的持久化顺序：意图落盘 → 目标写入 → 确认进度落盘 → 指针原子提交（tmp+rename+fsync）→ 关闭意图。
任意两点之间崩溃，`Open`/`Recover` 都会按序**前滚**未完成意图：

- 目标写成功但回执丢失：恢复时在目标侧发现同 hash 记录，直接确认并提交指针。
- 只写完一侧：指针提交到已确认的一侧，另一侧残留给 GC。
- 指针提交前退出：恢复时重写（幂等 Put）并提交指针。
- 指针已提交但意图未关闭：直接关闭意图，不重写。
- 恢复可重复执行任意多次（幂等）。

## 构建与测试

```sh
go build ./...
go test ./splitter/ -v     # 故障注入：丢回执 / 单侧写 / 提交前退出 / 重复恢复 / 延迟删除
```

## 可重放命令

```sh
go build -o splitter-bin ./cmd/splitter

# 事件为 JSONL：{"key","version","content","is_deleted"}，可反复重放（幂等）
./splitter-bin ingest -dir data -events events.jsonl
./splitter-bin get  -dir data -key order-1     # 指针视图：只看一个已确认位置
./splitter-bin scan -dir data                  # 全部键的指针视图
./splitter-bin scan-reference -dir data        # 参考实现：全量扫描两目标取最高版本
./splitter-bin snapshot -dir data create       # 固定当前指针集合（返回快照 ID）
./splitter-bin snapshot -dir data -id ID release
./splitter-bin gc -dir data                    # 回收无指针/快照引用的旧副本
./splitter-bin recover -dir data               # 手动重放未完成意图（Open 已自动执行）
```

注意：Go 标准 flag 解析要求 flags 位于位置参数之前（如 `snapshot -dir data create`）。

示例事件流（含乱序、重复、冲突、恢复）见 `testdata/events.jsonl`。
