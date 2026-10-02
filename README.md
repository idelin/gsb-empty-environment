# write-skew-guard：写倾斜检测与声明式约束断言器

将跨多行的业务不变量（如「医生值班表至少有 1 人值班」）声明为显式谓词断言；
事务在**提交临界区**内对相关约束进行重验，检测并发交叉修改（写倾斜，write skew）
导致的约束破坏，并精准中止其中一个事务、安全回滚。

## 组件

- `src/store.ts`
  - `Database`：MVCC 快照隔离存储（行级版本链 + 全局提交版本），串行化提交临界区；
  - `addConstraint({ name, tables, predicate })`：声明式约束注册表；
  - 提交协议：① 写-写冲突检测（first-committer-wins）→ ② 写倾斜检测
    （约束依赖表在快照之后被并发修改，或本事务写入该表时，在
    「最新已提交状态 + 本事务写入」合并视图上重验谓词）→ ③ 应用写入；
  - `ConstraintViolationError` / `WriteConflictError`：中止原因分类。
- `src/index.ts`：公共导出入口。
- `examples/doctor-oncall.ts`：验收场景运行样例。
- `test/`：Node.js 内置测试（`node:test` + `node:assert`）。

## 环境要求

Node.js ≥ 22.6（使用 `--experimental-strip-types` 直接运行 TypeScript，
仅使用可擦除类型语法；无需安装任何依赖）。

## 运行

```bash
npm test     # 12 个内置测试
npm run demo # 医生值班写倾斜演示
# 或直接使用高版本 node：
node --experimental-strip-types --no-warnings --test test/*.test.ts
node --experimental-strip-types --no-warnings examples/doctor-oncall.ts
```

## 验收场景

两位医生 A、B 同时值班（人数 = 2）。A 申请请假（快照中读到 B 在岗），
B 同时申请请假（快照中读到 A 在岗）。两事务并发提交时，先进入提交临界区者
重验通过（2 → 1），后者重验发现人数将跌为 0，违反不变量，被精准中止回滚。

演示输出：

```
[初始化] A、B 同时在值班，值班人数 = 2，不变量成立

[A] 快照 v1 读到值班名单: [A, B]
[B] 快照 v1 读到值班名单: [A, B]

--- 提交结果 ---
[A] 请假提交成功
[B] 被系统精准中止 -> 约束 "on_call.min_coverage" 在提交期重验失败，事务已中止回滚：值班表任意时刻至少有 1 名医生在岗

[最终状态] 值班名单: [B]，人数 = 1
[校验] 不变量「至少 1 人值班」在并发交叉修改后依然成立 ✔
```

## 测试覆盖（test/）

- 验收边界：A、B 同时请假，恰好一人成功、一人被 `ConstraintViolationError` 中止，最终人数 = 1；
- 确定性：后进入提交临界区者被中止；
- 无并发时单事务违反不变量同样被拦截（不误伤正常路径）；
- 互不相关的并发事务不产生误中止（按依赖表精准触发重验）；
- 写-写冲突 first-committer-wins；
- 快照隔离：事务读不到 begin 之后提交的并发写入；
- 回滚安全：被中止事务的所有写入（含无关表）不留痕迹；
- 约束断言器语义：删除行触发重验、多约束独立判定并报出约束名、
  read-your-writes、事务结束后禁止操作。
