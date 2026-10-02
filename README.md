# ARIES 崩溃恢复引擎 + 形式化约束巡检仪 (TypeScript)

数据库经典 ARIES 协议的离线实现：WAL 分析（Analysis）、历史重复（Redo）、
反向补偿（Undo + CLR）三阶段崩溃恢复，配套全量状态完整性巡检与不变式断言仪，
主动发现**悬挂引用**、**孤立环路**与**数据倾斜**。

## 环境要求

- Node.js ≥ 22.6（TypeScript 类型擦除直接运行，零依赖、零编译步骤；
  ≥ 23.6 时 `--experimental-strip-types` 可省略）
- 测试使用 Node.js 内置 `node:test` 运行器

## 运行

```bash
node --experimental-strip-types src/demo.ts        # 运行样例（崩溃 -> 三阶段恢复 -> 巡检）
node --experimental-strip-types --test test/*.test.ts   # 全部测试
# 或（PATH 中 node 满足版本要求时）
npm run demo
npm test
```

## 结构

- `src/log-record.ts` — WAL 记录模型（BEGIN/UPDATE/CLR/COMMIT/ABORT/END/CHECKPOINT）与序列化
- `src/wal.ts` — 预写日志：追加专用 JSONL 文件，异步互斥尾队列保证并发写安全，
  LSN 严格连续、prevLSN 事务链自动维护
- `src/storage.ts` — 页面存储（pageLSN 语义）
- `src/recovery.ts` — ARIES 三阶段恢复引擎
  - **Analysis**：定位最近 Checkpoint，以其事务表/脏页表为起点前向扫描至日志尾
  - **Redo**：从 DPT 最小 recLSN 起历史重复，pageLSN 已覆盖的记录跳过
  - **Undo**：按 LSN 逆序回滚全部 loser 事务，每个被撤销的 UPDATE 写一条 CLR
    （undoNextLSN 指向下一条待撤销记录），事务收尾写 END
- `src/inspector.ts` — 形式化约束巡检与不变式断言仪（纯函数、只读）
  - WAL 级不变式：LSN 唯一/连续、prevLSN 引用完整性（悬挂/乱序/跨事务）、
    CLR undoNextLSN 完整性、事务生命周期状态机、**孤立环路**检测、Checkpoint 引用有效性
  - 恢复后不变式：事务表无泄漏、loser 更新全部被 CLR 补偿、已提交事务无 CLR、
    loser 全部 END、pageLSN 一致性、**无数据倾斜**
  - 数据倾斜检测：将日志折叠为"仅已提交"期望态并与磁盘逐键比对
    （missing / diverged / unexpected 三类）
- `src/scenario.ts` — 验收场景构造器：3 个未提交事务（T3/T4/T5）+ 2 个已提交事务
  （T1/T2），Checkpoint 之后崩溃
- `src/demo.ts` — 端到端运行样例
- `test/` — node:test 测试（WAL、恢复、巡检边界条件、并发安全性）

## 验收场景

WAL 共 18 条记录：T1、T2 已提交；T3、T4、T5 在 Checkpoint（LSN=8）之后崩溃时
仍在运行。崩溃时 P1/P3 已刷盘、P2 脏页未刷盘。恢复后：

- 已提交修改持久化：`P1.a=10, P2.b=21, P3.c=30`
- 未提交修改回滚：T3 的 `P1.a=100`、`P1.d=7`，T4 的 `P3.c=300`，T5 的 `P2.b=210` 全部撤销
- Undo 写入 4 条 CLR（LSN 19/20/22/24）与 3 条 END
- 巡检仪断言：WAL 完整性 OK、恢复不变式 OK、零数据倾斜、事务表无泄漏
