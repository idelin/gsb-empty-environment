# kvimport — 可恢复的流式键值导入器

纯 Go 标准库实现。流式读取 `key=value\n` 记录，按批次确认 checkpoint，
坏记录就地隔离后继续，全部完成后通过一次原子 rename 发布结果。

## 记录格式

- 每行一条记录：`key=value\n`，`value` 中允许出现 `=`。
- 空行静默跳过；缺 `=`、空 key 的行写入 `rejects.log` 隔离，导入继续。
- **记录必须以 `\n` 结尾**。文件末尾未终结的残余字节视为截断尾
  （可能是写一半的记录），隔离进 `rejects.log`，绝不提交半条。

## 目录布局（全部在调用方指定的 `-dir` 内）

```
DIR/
  work/
    checkpoint.json   # 恢复锚点（tmp+fsync+rename+目录fsync 原子写入）
    data.wal          # 已确认记录的追加日志，批次末 fsync
    rejects.log       # 被隔离的坏记录（offset/原因/原文）
  store.db            # 唯一对外可见产物，仅在发布时原子 rename 出现
```

checkpoint 内容：输入路径、size、mtime、头部 64KiB 摘要、**最后确认偏移
Offset**、`input[0:Offset]` 的滚动 SHA-256、wal/rejects 的确认长度、Done 位。

## 崩溃恢复流程

1. 读 `checkpoint.json`；不存在则丢弃所有残留 work 文件从头开始
   （没有 checkpoint 等于什么都没确认过）。
2. 校验输入身份：路径、size、mtime、头部摘要，再重读 `input[0:Offset]`
   比对滚动摘要——**任一不符即拒绝恢复**（`InputChangedError`）。
3. 将 `data.wal` / `rejects.log` 截断到 checkpoint 记录的长度
   （崩溃点之后追加的未确认数据一律丢弃），从 wal 重建内存索引。
4. 从 `Offset` 继续流式读取，重复 批次→fsync→checkpoint 循环。
5. 输入消费完后：快照写 `store.db.tmp` → fsync → rename 为 `store.db`
   → 目录 fsync → 写 Done checkpoint。

**幂等性**：键的版本就是源记录在输入中的字节偏移。wal 截断 + 偏移续读
保证最后一块被原样重放；`Store.Apply` 对 `offset <= 已存版本` 的记录直接
忽略，因此重放最后一块不会造成重复键版本增长，恢复后的 `store.db` 与
无故障运行逐字节一致。

## 断电保证边界

**保证成立的前提**：

- 文件系统与硬件如实执行 `fsync`（不含谎报持久化的盘/固件）；
- 同一目录内的 `rename` 是原子的（tmp 与目标同文件系统）；
- 每个目录同一时刻只有一个导入进程（未实现进程间互斥锁）。

**保证内容**：

- 崩溃/断电后状态恰好回退到最近一次确认的 checkpoint；
  未 fsync 的 wal 尾部（含半条记录）按确认长度截断丢弃；
- `store.db` 要么不存在、要么是上一次完整发布的版本，
  任何时刻都观察不到未完成的批次；
- 末行截断的输入不会提交半条记录。

**不保证**：

- 输入在确认偏移之后被篡改且 mtime 被刻意保留（超出 size+mtime+头摘要+
  前缀摘要的检测能力）；
- 导入过程中输入被并发修改（恢复时会被检测并拒绝，但运行中不保证）；
- 多进程同时写同一目录。

## 使用

```sh
go run ./cmd/kvimport -dir DIR -input input.txt -batch 1000
go run ./cmd/kvimport -dir DIR -cat          # 查看已发布结果

# 故障注入（演示/测试）：在第 2 次 before_checkpoint 处模拟崩溃
go run ./cmd/kvimport -dir DIR -input input.txt -inject before_checkpoint:2
go run ./cmd/kvimport -dir DIR -input input.txt   # 去掉 -inject 重新运行即恢复
```

注入点：`before_checkpoint`（wal 已 fsync、checkpoint 未写）、
`after_checkpoint`、`before_publish`、`after_publish`。

## 测试

```sh
go test ./...
```

覆盖：正常导入与发布、坏记录隔离续跑、末行截断不提交、
wal 超前于 checkpoint 的崩溃恢复（重放不增长版本）、
发布前崩溃对读者不可见且旧版本不受影响、发布后崩溃幂等重发布、
输入变更（内容/大小/mtime）拒绝恢复、Done 后重复运行为空操作。
