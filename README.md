# CDC 反向补偿重放与数据回滚包生成器

基于 CDC（Change Data Capture）前置/后置镜像（Before/After Image）的灾备引擎：
捕获每条 DML 的完整前后镜像，在人为误操作后逆向生成补偿 DML 序列（回滚包），
重放后将数据恢复到误操作前的原貌。

纯 TypeScript 实现，零依赖；Node.js ≥ 23.6 原生类型擦除直接运行，
测试使用 Node 内置测试框架（`node:test`）。

## 结构

- `src/types.ts` — CDC 事件、补偿操作、回滚包类型定义
- `src/cdc-log.ts` — 追加式 CDC 日志，按事务 / 毫秒级时间窗口检索
- `src/store.ts` — 内存表存储，每条 DML 自动捕获 Before/After Image 写入 CDC 日志
- `src/undo-generator.ts` — 反向补偿生成器：INSERT→DELETE、DELETE→INSERT(前镜像)、UPDATE→UPDATE(前镜像)，按提交逆序排列
- `src/sql.ts` — 补偿操作的 SQL 渲染（含标识符/字面量转义）
- `src/engine.ts` — 回滚引擎：构建回滚包（按事务或时间窗口）并重放
- `examples/demo.ts` — 验收场景演示：误 UPDATE 清空 1000 条用户信息后自动冲正
- `test/` — `node:test` 测试（12 例）

## 运行

```bash
npm test     # node --test 'test/*.test.ts'
npm run demo # node examples/demo.ts
```

## 验收场景（examples/demo.ts）

1. 写入 1000 条用户记录（每条 INSERT 均捕获后镜像）；
2. 模拟误操作：`UPDATE users SET name='', email='', phone='', balance=0`（无 WHERE），
   1000 条记录被清空，每条 UPDATE 的前镜像进入 CDC 日志；
3. 引擎按事务读取 1000 条 Before Image，生成 1000 条反向赋值 SQL 组成回滚包；
4. 重放回滚包，逐字段恢复，校验恢复后与误操作前快照完全一致。

## 演示输出

```
[setup] seeded 1000 users, cdc events: 1000
[incident] tx tx-2 wiped 1000 rows (name/email/phone/balance)
[incident] sample row after wipe: {"id":1,"name":"","email":"","phone":"","balance":0}
[rollback] package rb-1: 1000 compensation ops
[rollback] first 3 reverse SQL statements:
           UPDATE "users" SET "name" = 'user-1000', "email" = 'user1000@example.com', "phone" = '13800001000', "balance" = 10000 WHERE "id" = 1000;
           UPDATE "users" SET "name" = 'user-999', "email" = 'user999@example.com', "phone" = '13800000999', "balance" = 9990 WHERE "id" = 999;
           UPDATE "users" SET "name" = 'user-998', "email" = 'user998@example.com', "phone" = '13800000998', "balance" = 9980 WHERE "id" = 998;
[rollback] replayed 1000 ops in 65.0 ms
[verify] restored state identical to pre-incident snapshot: true
[verify] sample row after restore: {"id":1,"name":"user-1","email":"user1@example.com","phone":"13800000001","balance":10}
```

## 测试结果

```
▶ RollbackEngine
  ✔ acceptance: restores 1000 users wiped by a mistaken UPDATE
  ✔ rolls back by time window, leaving earlier and later commits intact
  ✔ compensates DELETE by re-inserting the before image
  ✔ compensates INSERT by deleting the row
  ✔ unwinds chained updates to the same row in reverse order
  ✔ compensation writes are themselves captured in the CDC log
  ✔ throws for unknown transactions and empty windows
▶ generateRollbackPackage
  ✔ inverts UPDATE back to the before image
  ✔ inverts INSERT into DELETE and DELETE into INSERT of before image
  ✔ compensates chained updates to the same row in reverse order
  ✔ escapes single quotes in SQL literals
  ✔ throws when a before image is missing

ℹ tests 12  ℹ pass 12  ℹ fail 0
```

## 设计要点

- **逆序补偿**：同一行被连续修改时，按 seq 逆序回放前镜像，保证链式变更正确展开。
- **毫秒级窗口**：CDC 事件携带毫秒提交时间戳，支持 `(fromMs, toMs]` 精确窗口回滚。
- **可审计**：补偿写入本身也经过 TableStore DML，再次进入 CDC 日志，可二次冲正。
- **SQL/API 双形态**：每个补偿操作同时携带结构化 image（API 调用）与渲染好的 SQL 语句。
