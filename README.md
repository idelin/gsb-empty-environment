# Protobuf Wire-format 解析器 + 多租户 DRR 公平调度器

零外部依赖的 TypeScript 实现，直接由 Node.js 原生 TypeScript（type stripping）运行，
测试使用 Node 内置 node:test。

## 环境要求

- Node.js **>= 23.6**（原生运行 .ts；22.6+ 可加 --experimental-strip-types 运行）
- 无任何 npm 依赖（package.json 无 dependencies）

## 运行

    node --test test/protobuf.test.ts test/scheduler.test.ts   # 或 npm test
    node demo/demo.ts                                          # 或 npm run demo

## 组件一：Protobuf 二进制 Wire-format 解析器（src/protobuf/）

- varint.ts — Varint 7 位压缩编解码（64 位 BigInt 精度，10 字节上限）、ZigZag 有符号映射
- reader.ts — WireReader：Tag / Varint / Fixed32 / Fixed64 / Float / Double /
  Length-delimited 读取，每一次读取都做边界检查，截断即抛 WireError（安全越界防护）
- writer.ts — WireWriter：构造合法 Wire-format 字节流（测试与样例使用）
- schema.ts — 反射模式校验引擎：以 MessageSchema 描述消息结构，
  decodeMessage 递归还原嵌套消息、Packed/非 Packed Repeated 数组；
  支持 strict 模式（未知字段报错）、嵌套深度上限（防资源耗尽）、
  UTF-8 严格校验、wire type 与字段类型匹配校验；validateMessage 返回结构化校验结果

## 组件二：多租户隔离与配额公平调度器（src/scheduler/scheduler.ts）

- **资源隔离**：每租户独立队列、独立并发上限（maxConcurrency）、
  队列上限（maxQueueSize，超限抛 QuotaExceededError）
- **配额限流**：每租户令牌桶（quotaTokens / refillIntervalMs），
  惰性补充、不累积超发；耗尽即暂停该租户，其他租户照常调度
- **Deficit Round Robin**：按 weight 分配 quantum，deficit 计数器跨轮次累积，
  队列排空即归零（防无限信用累积）；全局 maxInflight 模拟共享工作池容量
- **并发安全**：pump 重入保护；drain() 等待全部任务落定；
  assertNoLeaks() 严格断言无残留队列 / 在途任务 / 活跃链表节点 / 未兑现等待者
- 时钟可注入（now），配额与公平性测试完全确定性

## 测试

32 个用例覆盖：varint/zigzag/fixed 边界值与截断防护、三层嵌套、packed 数组、
未知字段跳过与 strict 模式、非法 wire type、UTF-8 校验、深度上限；
DRR 精确派发序列（权重 1:2）、配额限流与恢复、并发上限隔离、队列拒绝、
异常传播、5 租户 x 200 任务并发压力 + 严格无泄漏断言。
