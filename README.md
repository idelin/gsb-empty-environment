# MVCC 版本链管理与垃圾回收器

TypeScript 实现的可重复读 MVCC 多版本链管理与 GC 修剪器，零依赖，
使用 Node.js 内置测试运行器（`node:test`）验证。

## 设计

- **版本链**：每个数据项维护一条按更新时间倒序（新 → 旧）链接的版本链表
  （`src/version-chain.ts` 中的 `Version` 节点）。
- **可重复读**：只读事务通过 `beginRead(readTs)` 登记 ReadView；读操作沿链表
  找到第一个 `ts <= readTs` 的版本，同一 ReadView 内读取结果稳定。
- **ReadView 追踪**：`minActiveReadTs` 实时给出全局最小活跃 ReadView，
  作为 GC 水位线。
- **GC 修剪**：`gc()` 对每条链保留"不晚于水位线的最新版本"及其以新部分，
  截断并释放全部更旧的废弃版本；无活跃读者时只保留最新版本。
- **安全性假设**：读时间戳来自单调递增的全局时钟，未来读者的 `readTs`
  不会小于当前最小活跃 ReadView，因此被修剪的版本对任何活跃/未来读者
  均不可见。

## 运行要求

Node.js >= 22.6（利用内置 TypeScript 类型擦除直接运行 `.ts`，无需编译）。

```bash
npm test    # 运行全部测试（node:test）
npm run demo # 运行验收场景演示
```

## 验收场景

数据项 X 有版本 `v1(t=1), v2(t=5), v3(t=10)`，活跃最小 ReadView 为 `t=8`：

1. `gc()` 修剪掉 `v1`（被 `v2` 覆盖且对 `t>=8` 的读者不可见），释放内存；
2. `t=8` 的活跃读者仍读到 `v2`，`t>=10` 的读者仍读到 `v3`；
3. 读者结束后水位线消失，下一轮 `gc()` 继续回收 `v2`，只保留 `v3`。

对应测试见 `test/version-chain.test.ts` 的「GC 修剪（验收边界）」分组。
