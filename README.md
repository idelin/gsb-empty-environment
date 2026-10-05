# mergetool — 可恢复的多路文件归并

记录为 `(key, source, sequence)` + 不透明 payload，按 **key → source → sequence**
稳定全序做 k 路归并。支持 checkpoint 断点续跑，崩溃恢复后**不漏记、不重复输出**。

## 构建与测试

```sh
go test ./merge/          # 含故障注入测试
go build ./cmd/mergetool
```

## 用法

```sh
# 生成有序输入（每个 run 内按 key 有序，seq 递增）
mergetool gen -dir data -sources 8 -records 5000 -maxkey 20000

# 归并（自动从 checkpoint 续跑；重复执行是幂等 no-op）
mergetool merge -o out.run -ckpt out.ckpt -mem 256KB -block 8KB data/input-*.run

# 全量排序核对：输出记录序列 + checkpoint 中各源偏移
mergetool verify -o out.run -ckpt out.ckpt data/input-*.run
```

## 设计

**记录格式**（`merge/record.go`）：varint 帧长 + `keyLen key | source | seq | payloadLen payload`。

**内存限额**（`merge/merger.go`）：按实际缓冲字节记账（各源读缓冲 + 堆中在途记录 +
输出缓冲），`MemLimit` 约束的是预读缓冲；k 路归并固有的"每源一条在堆记录"开销
在限额之外。读出按 `BlockSize` 分块，但一旦解析到帧长，**当前记录必须读完**——按
剩余字节精确读取，因此单条记录可以大于普通块、甚至大于整个限额。

**归并不变量**：弹出堆顶前，每个未耗尽的源恰好有一条记录在堆中，保证全局有序。

**Checkpoint 与崩溃恢复**：
1. 刷输出缓冲并 `fsync` 输出文件；
2. 写 checkpoint（临时文件 + `fsync` + `rename` + 目录 `fsync`），确认输出偏移
   `OutputOffset` 与每个源的已消费偏移 `InputOffsets`。
两步之间崩溃即"输出确认丢失"：输出文件里存在未被确认的尾部字节。恢复时输出
**截断到 `OutputOffset`**、各源 seek 到 `InputOffsets` 重读，因此恰好一次。

**输入截断**：文件尾部的半条记录（`ErrIncomplete`）被丢弃并标记 `Truncated`，
不计入已消费偏移；核对时同样只取完整记录。

**核对**（`merge/verify.go`）：读全部输入的完整记录做一次全量排序，与输出逐条
比对；同时校验 checkpoint 的输出偏移/记录数与每个源的消费偏移/记录数。

## 故障注入测试（`merge/merger_test.go`）

- `TestCrashAckLoss`：在输出 fsync 后、checkpoint 确认前崩溃，验证截断未确认输出后续跑不重不漏；
- `TestInputTruncation`：输入尾部截断半条记录；
- `TestCrashLoopWithTruncation`：随机崩溃点循环续跑，并在崩溃与恢复之间注入输入截断；
- `TestOversizeRecord`：单条记录大于块与限额；
- `TestResumeAfterDone`：完成后重跑返回 `ErrDone`，输出不变。
