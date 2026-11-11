# imapsync

邮件采集器的 IMAP 增量同步包。传输层只依赖 `io.ReadWriter`，可接 TCP/TLS
连接或内存管道。

## 结构

- `parser.go` — IMAP 响应解析：字面量（`{n}`，可跨多次底层读取，经
  `io.ReadFull` 重组）、引号字符串、括号列表、`[...]` 响应码。
- `client.go` — 最小 IMAP4rev1 客户端：问候、带标签命令、SELECT
  (CONDSTORE)、UID FETCH（含 CHANGEDSINCE）、UID SEARCH。
- `sync.go` — 同步器与状态存储。

## 同步策略

1. `SELECT folder (CONDSTORE)` 读取 UIDVALIDITY 与 HIGHESTMODSEQ。
2. UIDVALIDITY 与本地状态不符（或首次同步）→ 丢弃缓存，全量
   `UID FETCH 1:* (FLAGS)` 重建。
3. 否则用保存的 MODSEQ 发 `UID FETCH 1:* (FLAGS) (CHANGEDSINCE n)`
   增量拉取标志变化；服务器拒绝过期 MODSEQ（NO/BAD）时回退全量，
   传输层错误则直接失败（连接已死，重试无意义）。
4. 同步中途到达的新邮件会推高 untagged `EXISTS`，触发额外一轮
   CHANGEDSINCE，直到文件夹安静为止。
5. `UID SEARCH ALL` 对齐现存活 UID 集合，权威地清理同步中途
   `EXPUNGE` 掉的邮件。
6. 每个文件夹的状态（UIDVALIDITY、MODSEQ、UID→标志）先写临时文件、
   fsync、再 rename 原子落盘；崩溃不会留下写了一半的状态。

`Syncer.Dial` 每个文件夹新建连接，单个文件夹同步中断只影响该文件夹，
下次同步从其上次原子保存的 MODSEQ 断点续传。

## 测试

```sh
go test ./...
```

测试使用脚本化的内存模拟服务器（`mock_test.go`）：按脚本校验客户端
命令、回放响应（可分片写入以模拟跨多次读取的字面量）、可中途断连。
覆盖断点恢复（连接中断后从已保存的 MODSEQ 续传）、UIDVALIDITY 变化
触发重建、同步中途的 EXPUNGE 与新到邮件。
