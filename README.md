# cdcbarrier — DDL/DML 时间线协调器

支持在线表结构变更的 CDC 处理器。当 DDL 事件（如 `AlterTableAddColumn`）
到达时，协调器在数据流中插入**全局屏障**：

1. **排空（drain）**：屏障前所有已分发的旧结构 DML 必须解析并发送完毕；
2. **切换元数据缓存**：将新 Schema 应用到注册表，版本号 +1；
3. **恢复流**：屏障后的 DML 绑定新 Schema 快照解析。

## 关键设计

- `Registry`（`coordinator/schema.go`）：带版本号的元数据缓存，`Get` 返回
  深拷贝快照，快照不可变。
- `Coordinator.Run`（`coordinator/coordinator.go`）：单 goroutine 顺序消费
  事件流，DML 在**分发时刻**绑定 Schema 快照（即"切断元数据缓存"），随后由
  worker 池并发解析；DDL 到达时 `inflight.Wait()` 排空后再应用新 Schema，
  并向下游发出 `BarrierOutput`。
- 字段数与 Schema 不匹配时返回 `FieldCountError`，绝不静默错位。

## 运行

```sh
go run ./cmd/demo     # 运行样例
go test ./...         # 运行测试
go test -race ./...   # 竞态检测
```

## 验收边界

流：`DML_Old(3列) -> DDL(增加col4) -> DML_New(4列)`

`TestCoordinatorCutsSchemaAtDDL` 断言：DML_Old 按 schema v1（3 列）解析、
不含 col4；屏障记录 v1→v2；DML_New 按 schema v2（4 列）解析、col4 对齐到
正确字段，零字段错位。
