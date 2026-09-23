# reactive-snapshot-store

自研的极简响应式状态库（TypeScript，零依赖，不用任何现成响应式框架），
支持快照保存 / 原子恢复 / 依赖证据重建计算图 / 篡改检测。

## 设计要点

- **状态节点 + 计算节点**：计算节点只保存定义标识 `(defId, defVersion)` 与依赖边；
  函数本体注册在 `Registry`，**绝不直接序列化执行**。
- **快照格式**（`src/serialize.ts`）：`schemaVersion` + 节点列表 + 可选 sha256 完整性摘要。
- **两阶段恢复**（`stageRestore`）：先落位纯状态节点，再按保存的依赖证据重建计算图；
  全部校验在暂存区完成，**未知定义或版本不符即阻止原子发布**，live store 不被触碰。
- **三类检查分开**（`src/graph.ts`）：
  - 共享身份：同一 id 不得重复，所有引用共享同一节点记录；
  - 单调环：仅 `monotonic: true` 的定义允许指向自身的反馈边（求值时取上一轮提交值）；
  - 非法求值环：去掉合法单调自边后，计算节点间任何环都拒绝。
- **订阅回调在完整恢复前不运行**：恢复是单次原子指针替换，回调只在发布成功后触发一次。
- **事务与回放**：所有变更按提交批次记入操作日志，`Store.replay` 逐批回放，
  单调语义逐批一致；读者只能看到已提交快照，**看不到事务中间态**。
- **共享实体恢复后仍共享**：同一 id 在恢复后的图中只有一份记录，一处 `set` 驱动所有依赖者。
- **schema 版本不兼容直接拒绝**；篡改节点 / 版本 / 依赖边被完整性摘要或结构性校验拦截，
  **旧存活状态在失败恢复后保持可读**。

## 运行

```bash
npm test    # Node.js 内置测试运行器（node:test，原生类型擦除，无需编译）
npm run demo
```

要求 Node.js >= 23（TypeScript 类型擦除开箱即用）。

## 运行样例（`npm run demo` 摘要）

```
== 初始提交 ==
  a=1 b=2 sum=3 product=2 total=1
== 原子事务 set(a=10), set(b=20) ==
  [订阅回调] sum: 3 -> 30
  sum=30 product=200 total=11
== 序列化快照 ==
  {"schemaVersion":1,"nodes":[
    {"id":"a","kind":"state","value":10},
    {"id":"sum","kind":"computed","def":{"id":"add","version":1},"deps":["a","b"],"carried":30},
    {"id":"total","kind":"computed","def":{"id":"acc","version":1},"deps":["total","a"],"carried":11}],
   "integrity":"6534…4c65"}
== 操作日志回放 == sum=30 total=11
== 恢复 == sum=30 total=11（单调节点保持携带值，不重复累积）
== 篡改依赖边被拒绝 == INTEGRITY: snapshot integrity digest mismatch (tampered payload)
  旧状态仍可读: sum=30
== 定义版本不符被拒绝 == DEFINITION_VERSION
== 非法求值环被拒绝 == ILLEGAL_CYCLE: illegal evaluation cycle: x -> y -> x
== schema 版本不兼容被拒绝 == SCHEMA_VERSION: unsupported schemaVersion: 999 (expected 1)
```

## 测试结果（`npm test`）

38 项测试全部通过：

- `tests/store.test.ts`（12）：事务原子性、读者隔离、失败回滚、操作日志回放、
  单调环累积、非法环/自反馈边/重复 id 拒绝、订阅语义。
- `tests/serialize.test.ts`（6）：序列化往返、函数不落盘、共享实体恢复后仍共享、
  恢复后继续事务、含 restore 批次的日志回放。
- `tests/restore.test.ts`（13）：schema 版本、未知定义、版本不符、缺节点、
  非法求值环 vs 单调环、重复 id、恢复前回调不运行、失败恢复后旧状态可读。
- `tests/tamper.test.ts`（7）：篡改节点值 / 节点 id / 定义版本 / 依赖边均被拒绝，
  去掉完整性字段后结构性校验依然拦截，多次失败恢复后 store 正常工作。

```
ℹ tests 38
ℹ pass 38
ℹ fail 0
```

## 目录结构

```
src/
  errors.ts     # RestoreError 与错误码
  registry.ts   # 计算定义注册表（id + version -> 函数）
  graph.ts      # 记录模型、三类图检查、拓扑重算
  store.ts      # Store / Transaction：原子提交、操作日志、订阅
  serialize.ts  # 快照格式、完整性摘要、两阶段暂存恢复
tests/          # node:test 测试（38 项）
examples/demo.ts
```
