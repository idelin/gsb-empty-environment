# pbwire-arbiter

零外部依赖的 TypeScript 实现，包含三个组件：

1. **Protobuf 二进制 Wire-format 编解码器** — 纯手工解析，支持 Varint（7 位压缩）、ZigZag 有符号整数、Fixed32/64、嵌套 Length-delimited 消息、Packed Repeated 数组与未知字段跳过。
2. **反射模式校验引擎** — 运行时 Schema 描述符 + 递归结构校验，报告完整字段路径的所有问题。
3. **多节点并发竞争与冲突仲裁器** — 原子性（同步应用、不可中断）、线性一致性（单序列化队列 + 单调序列号）、无锁 Promise 链（无死锁）、单线程应用步（无竞态）、操作注册表归零（无泄漏）。

## 环境要求

- Node.js >= 22.6（22.x 需 `--experimental-strip-types`，23.6+ 原生支持 TS 类型擦除）。
- 仓库自带 `scripts/run-tests.mjs` / `scripts/run.mjs`：当前 Node 过旧时自动在 nvm 目录中寻找新版 Node 重 exec，因此直接 `npm test` 即可。

## 运行

```bash
npm test        # node:test 内置测试运行器，37 个用例
npm run demo    # 端到端样例：编码 -> 解码 -> 校验 -> 截断防护 -> 并发仲裁
```

## 结构

```
src/varint.ts     Varint/ZigZag 编解码，10 字节上限与截断防护
src/wire.ts       边界检查的 WireReader / WireWriter
src/schema.ts     反射 Schema 描述符与校验引擎
src/protobuf.ts   Schema 驱动的消息编解码（嵌套 / packed / 未知字段跳过）
src/arbiter.ts    多节点竞争与冲突仲裁器（CAS 事务 + 乐观重试）
tests/            node:test 测试（varint / protobuf / schema / arbiter）
examples/demo.ts  运行样例
scripts/          零依赖测试/样例启动器
```

## 设计要点

### 安全越界防护
每次读取前先校验剩余容量；截断输入抛出 `TruncatedMessageError`（携带
`needed`/`available`），绝不产生原生 `RangeError`。测试对一条完整消息的
**每一个截断点**逐一验证。

### 线性一致性
仲裁器将所有操作排入单条 Promise 链；每个操作的线性化点是其同步应用
的瞬间，提交时分配唯一单调递增的 `seq`。测试断言：并发提交重放后
`seq` 稠密连续、版本链 `1..N` 无缺口，最终状态与某一串行执行等价。

### 无死锁 / 无竞态 / 无泄漏
- 无锁：队列即 Promise 链，操作只等待自己的轮次，不持有跨 await 的锁。
- 无竞态：共享状态仅在单线程事件循环的同步应用步内变更。
- 无泄漏：每个操作 settle 恰好一次；`liveOps` 注册表与 `pending`
  计数在 `drain()` 后必须归零，测试同时断言进程活动句柄数不变。

## 测试结果

```
# tests 37
# pass 37
# fail 0
# duration_ms ~150
```

覆盖：varint 边界值/截断/溢出、zigzag 极值、fixed32/64 字节序、
多层嵌套、packed/unpacked 兼容、未知字段跳过、逐字节截断防护、
Schema 自校验与结构校验、N 节点 M 次竞争的线性一致性、CAS 冲突
仲裁唯一胜者、多键事务全或无、watchdog 无死锁、注册表零泄漏。
