# occ-engine

无阻塞自旋乐观并发控制（OCC）引擎，TypeScript 实现，零运行时依赖，
使用 Node.js 内置测试运行器（`node:test`）验证。

## 设计

经典 OCC 三阶段执行内核：

1. **读取阶段**：事务在私有 `ReadSet`（键 → 读取时观察到的提交版本）与
   `WriteSet`（键 → 待写入新值）上工作，不加锁、不阻塞；支持
   read-your-writes；读取不存在的键记录版本 0，用于检测幻读。
2. **验证阶段**：提交时检查 `ReadSet` 中每个键的当前版本是否仍与读取时一致，
   即未被其他已提交事务修改；精准返回过期键列表。
3. **写入阶段**：验证通过则把整个 `WriteSet` 以同一个全局单调递增的提交版本
   一次性批量提交；验证失败则丢弃 `WriteSet`，干净利落回滚，存储零副作用。

“验证 + 写入”临界区由 `SpinLatch` 保护：Node.js 单线程模型下该临界区是
同步代码、天然原子，闩锁永不真正自旋（重入会直接抛错，作为并发缺陷断言）；
移植到多线程运行时（`worker_threads` + `SharedArrayBuffer`）时对应
`Atomics` 自旋等待。读取路径完全无锁。

冲突处理：`engine.run()` 提供指数退避 + 随机抖动的自动重试；
`engine.tryRun()` / `begin()` + `commit()` 提供不重试的手动控制。
引擎内置冲突统计：尝试 / 提交 / 中止次数、冲突率、按键冲突分布。

## 目录结构

```
src/occ/store.ts        版本化键值存储（全局提交序列号）
src/occ/transaction.ts  事务上下文：ReadSet / WriteSet / validate()
src/occ/engine.ts       三阶段执行内核 + 重试 + 冲突统计
src/occ/spin-latch.ts   提交临界区自旋闩锁
src/occ/errors.ts       OccConflictError（携带冲突键列表）
src/lock/lock-engine.ts 锁方案基线（全局互斥锁，用于吞吐对比）
test/                   node:test 测试（正确性 / 并发 / 吞吐对比）
examples/demo.ts        运行样例
bench/compare.ts        OCC vs 锁方案基准对比
```

## 运行

要求 Node.js >= 22.6（使用内置类型剥离直接运行 TypeScript，无需编译）：

```bash
nvm use            # 22.14.0
npm test           # node:test 内置测试
npm run demo       # 运行样例
npm run bench      # OCC vs 锁方案吞吐对比
npm run typecheck  # tsc --noEmit（需本地可用 typescript）
```

## 测试结果（实测）

```
ok 1 - 读多写少场景：OCC 吞吐大幅领先锁方案
ok 2 - 高并发单一热点键：全部提交且最终值精确，冲突率被统计
ok 3 - 高并发不同热点键（无重叠写）：零冲突全部提交
ok 4 - 混合负载：多热点键读写并发，提交计数与数据总量一致
ok 5 - 三阶段执行：无冲突时写入阶段一次性批量提交，结果对其他事务可见
ok 6 - 读取阶段可读到本事务未提交的写入（read-your-writes）
ok 7 - 验证阶段精准识别 ReadSet 版本过期，回滚干净利落
ok 8 - 验证仅报告真正过期的键，不误伤其他键
ok 9 - 幻读检测：读取时不存在的键被其他事务创建，验证失败
ok 10 - run 自动重试：冲突后重放事务并最终提交，冲突率统计准确
ok 11 - 只读事务通过验证但不产生新的提交版本
# tests 11
# pass 11
# fail 0
```

## 运行样例（`npm run demo`）

```
== 场景 1：重叠写入触发验证失败 ==
初始状态: balance=100, bonus=0
Alice 读取阶段: balance=100
Bob   读取阶段: balance=100
Alice 验证通过，批量提交: balance=80, bonus=5
Bob 验证失败: ReadSet 中键 [balance] 版本已过期，事务整体回滚
回滚后存储: balance=80, bonus=5（Bob 的写入无一生效）

== 场景 2：冲突后自动重试 ==
重试后提交成功: balance=90（本次无冲突，尝试 1 次）

== 场景 3：100 个并发事务争抢同一热点键 ==
counter=100（期望 100，无丢失更新）
尝试=1105 提交=100 中止=1005 冲突率=91.0%
```

## 基准对比（`npm run bench`，每个操作模拟 1ms I/O 延迟）

```
### 场景 A：读多写少（90% 读 / 10% 写，32 并发，128 键，8 个热点键）
OCC  :    779.8 ms |     246 txn/s | 尝试=206 提交=192 中止=14 冲突率=6.8%
LOCK :   5737.2 ms |      33 txn/s | 串行执行，无冲突概念
吞吐加速比: 7.4x

### 场景 B：写热点（100% 写同一热点键，48 并发）
OCC  :   1803.4 ms |      53 txn/s | 尝试=617 提交=96 中止=521 冲突率=84.4%
LOCK :    371.7 ms |     258 txn/s | 串行执行，无冲突概念
吞吐加速比: 0.2x
```

结论符合 OCC 理论特性：读多写少时读取路径无锁、高度并行，吞吐大幅领先
锁方案；极端写热点下冲突率飙升、重试代价高，锁方案反而更优。
两个场景均通过数据一致性校验（OCC 与锁方案最终结果一致，无丢失更新）。
