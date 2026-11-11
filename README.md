# pgoutput

Go 实现的 PostgreSQL 逻辑复制 `pgoutput` 消息解码与事务组装包，面向数据同步服务。

## 能力

- **消息解码**：解析 `B/C/O/R/Y/I/U/D/T/M/m` 以及流式 `S/E/c/A` 和主备 keepalive `k`，
  支持直接传入 pgoutput 负载或完整的 CopyData(`w`) 帧；所有读取带边界检查，坏帧返回错误。
- **Relation / Type 缓存**：`Catalog` 缓存表结构（列、主键标识、类型 OID、typmod）和类型信息，
  后续行变更按缓存解析与宽度校验，未知 relation 直接报错。
- **未变更 TOAST 列**：旧元组中的 `u` 标记列用新元组的当前值补齐，不会被当成 NULL；
  两侧都缺失时保留 `Unchanged` 标记。`K` 形式的仅主键旧元组会扩展为整表宽度。
- **流式大事务（协议 v2，兼容 v4 子事务帧）**：
  - `Stream Start/Stop` 分段缓冲，多个事务的分段可以交错，行消息按 XID 路由回各自缓冲区；
  - `Stream Commit` 才发布事务，`Stream Abort` 丢弃整个顶层事务；
  - v2 下子事务中止会使整个顶层事务失效，服务端随后以首段重新流式发送，包按此语义处理；
  - 对带 subxid 的流式帧，仅回滚对应子事务的变更，其余变更按原线路顺序保留。
- **提交顺序与 LSN 水位**：事务只在提交后发布，严格遵循线路上的提交顺序；
  `SafeLSN()` 只推进到已发布事务的 EndLSN，`CheckAck()` 阻止确认越过仍在组装的事务。

## 用法

```go
a := pgoutput.NewAssembler()

for frame := range replicationFrames { // frame 为 CopyData 字节
    msg, err := pgoutput.Parse(frame) // 自动识别 'w' 包装
    if err != nil {
        return err
    }
    res, err := a.Handle(msg)
    if err != nil {
        return err
    }
    for _, txn := range res.Transactions { // 已提交、按提交顺序排列
        if err := apply(txn); err != nil {
            return err
        }
    }
    if err := a.CheckAck(a.SafeLSN()); err != nil { // 永远不会越过未发布事务
        return err
    }
    standbyConfirm(a.SafeLSN())
}
```

## 测试

测试用与服务端一致的 wire 编码构造消息序列，覆盖：

- 全部消息类型的编解码往返、截断帧/未知类型/尾部多余字节等错误路径；
- TOAST 合并、仅主键旧元组扩展、Relation/Type 缓存查找；
- 两个流式事务分段交错、中间穿插普通事务与高 LSN keepalive 的提交顺序与水位；
- v2 顶层中止后首段重放、subxid 子事务回滚（含同一条行先插后删再回滚）；
- 协议状态机错误（嵌套分段、未知 XID 提交/中止、事务外写行等）。

运行：`go test ./...`（含 `-race` 验证）。
