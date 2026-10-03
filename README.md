# 反向补偿重放与数据回滚包生成器（CDC Rollback Engine）

基于 CDC（Change Data Capture）Before/After 镜像的灾备引擎：捕获每条变更的前置镜像
（Before Image）与后置镜像（After Image），在人为误操作后逆向生成补偿 DML 序列，
执行快速冲正，将数据恢复到误操作发生前的毫秒级原貌。

## 运行环境

- Node.js >= 23.6（原生 TypeScript type-stripping，零依赖，无需编译）
- 测试使用 Node 内置 `node:test`

## 快速开始

```bash
npm test          # 运行全部测试（node:test）
npm run sample    # 运行验收样例：1000 条用户被误清空 -> 自动冲正复原
```

## 架构

| 模块 | 职责 |
| --- | --- |
| `src/types.ts` | CDC 事件 / 补偿 DML 类型定义 |
| `src/store.ts` | 内存表存储，写入时逐行捕获 Before/After 镜像并发布 CDC 事件（亚毫秒时间戳） |
| `src/compensator.ts` | 反向补偿生成：INSERT→DELETE、DELETE→INSERT、UPDATE→反向赋值；按 sequence LIFO 逆序 |
| `src/sql-renderer.ts` | 补偿 DML 渲染为可执行 SQL（标识符引号、字面量转义、事务包裹） |
| `src/rollback-package.ts` | 回滚包生成与落盘：`manifest.json`（含 SHA-256 校验和）/ `statements.json` / `rollback.sql` |
| `src/replay.ts` | 补偿回放引擎：将冲正 DML 逐条应用到目标表 |

## 补偿映射规则

- 原 `INSERT` → 补偿 `DELETE`（按主键删除误插入行）
- 原 `DELETE` → 补偿 `INSERT`（用 Before Image 重建被删行）
- 原 `UPDATE` → 补偿 `UPDATE`（仅将发生变化的列恢复为 Before Image 值，WHERE 走主键）

同一行多次变更按 sequence 逆序（LIFO）逐层撤销。支持按 `txId` 或毫秒时间窗
（`fromMs`/`toMs`）筛选待冲正事件。

## 验收样例（examples/run-sample.ts）

1. 初始化 1000 条用户数据（CDC 捕获开启）；
2. 误操作事务执行 `UPDATE users SET name=NULL, email='', balance=0`，1000 行被清空；
3. 引擎读取 CDC 日志 Before Image，生成 1000 条反向赋值 SQL 的回滚包并落盘；
4. 回放补偿序列，`assert.deepStrictEqual` 校验全表恢复到误操作前的毫秒级原貌。

实测输出（本机）：回滚包生成约 20ms，1000 条补偿回放约 55ms。
