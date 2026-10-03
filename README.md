# ARIES 崩溃恢复引擎（Go，仅标准库）

实现数据库经典 **ARIES** 预写日志（WAL）与崩溃恢复协议：三阶段扫描
（Analysis / Redo / Undo）、紧凑二进制日志编解码、内存映射式零拷贝解析、
向前/向后兼容与全路径边界防溢出。

## 结构

| 文件 | 职责 |
|---|---|
| `record.go` | 日志记录二进制编解码（UPDATE/CLR/COMMIT/ABORT/END/BEGIN/END_CHECKPOINT），零拷贝解码，版本兼容，边界校验 |
| `wal.go` | WAL 追加写入器（并发安全）+ `WALImage` 一次性映射整个日志到内存、原地零拷贝扫描；崩溃撕裂写（torn tail）自动截断 |
| `pagestore.go` | 4KB 页面缓冲池 + 磁盘快照持久化，`pageLSN` 簿记，支持单页刷盘模拟崩溃 |
| `recover.go` | 三阶段恢复：Analysis 重建 TT/DPT；Redo 从最小 recLSN 历史重复；Undo 以最大 LSN 堆反向扫描、写 CLR（undoNextLSN 链）与 END |
| `cmd/ariesdemo` | 运行样例：5 事务 + checkpoint 后崩溃 → 三阶段恢复全过程打印 |

## 二进制格式

```
文件头:  magic(4)="ARIS" version(2) flags(2)
记录:    type(1) payloadLen(4) payload...
UPDATE:  txnID(8) prevLSN(8) pageID(4) offset(4) oldLen(4) newLen(4) old new
CLR:     同 UPDATE + undoNextLSN(8)          -- v2 新增
事务记录: txnID(8) prevLSN(8)                -- COMMIT/ABORT/END
END_CKPT: beginLSN(8) txnCount(4) txn*(17) dptCount(4) dpt*(12)
```

- **LSN 即文件偏移**，单调递增，记录内不冗余存储。
- **零拷贝**：`DecodeRecord` 返回的 `OldData/NewData` 直接子切片源缓冲区，
  不复制任何负载字节（测试通过修改源缓冲区验证别名）。
- **向前兼容**：长度前缀跳过未知尾部字段与未知记录类型。
- **向后兼容**：v1 CLR 无 `undoNextLSN`，解码时回退为 `prevLSN` 链。
- **防溢出**：所有长度字段先与缓冲区校验再切片；模糊测试 50 万次执行无 panic。

## 运行

```bash
go run ./cmd/ariesdemo     # 崩溃恢复全过程演示
go test ./...              # 全部测试
go test -race ./...        # 并发安全（race detector）
go test -fuzz=FuzzDecodeRecord -fuzztime=30s .
```

## 测试覆盖

- **编解码**：全类型往返、空负载、最大长度、零拷贝别名验证。
- **边界防溢出**：短头、payloadLen 溢出、内层长度溢出、checkpoint 计数溢出、
  CLR 截断、所有截断点无 panic、fuzz。
- **兼容性**：未知尾部字段跳过、未知记录类型跳过、v1 CLR 回退。
- **WAL**：并发追加（8×100，race 通过）、LSN 单调且等于文件偏移、
  撕裂写截断、关闭后拒绝写入、坏文件头。
- **页面存储**：页边界写、持久化往返、部分刷盘（崩溃语义）、并发读写。
- **验收场景**：3 个未提交事务（T1/T3/T5）+ 2 个已提交事务（T2/T4），
  checkpoint 之后崩溃；Analysis 精确重建 TT（T2 被 END 移除）与 DPT；
  Redo 历史重复（含败者修改）；Undo 写 5 条 CLR + 3 条 END，
  未提交修改全部回滚、已提交数据持久化；重启恢复幂等（零新增日志）。
- **无泄漏**：恢复前后 goroutine 数一致，Close 后无脏页残留、资源拒绝复用。
