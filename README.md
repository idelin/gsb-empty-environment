# Protobuf Wire Codec + Saga Compensation Orchestrator (TypeScript)

零外部依赖。两个组件：

1. **二进制紧凑变长编解码器 + 反射模式校验引擎**（`src/wire.ts`, `src/schema.ts`）
   纯手工解析 Protocol Buffers 二进制 wire format：Varint 7 位压缩、ZigZag 有符号
   整数、Fixed32/64（小端）、Length-delimited 嵌套消息、Packed repeated 数组。
   每次读取都做边界检查：字段截断时抛出携带偏移量的 `DecodeError`，绝不越界读取。
   Schema 引擎按声明式描述还原结构体字段，未知字段安全跳过，wire-type 不匹配
   抛出带字段路径的 `SchemaError`。

2. **反向补偿与事务回滚状态编排**（`src/saga.ts`）
   Saga 编排器：步骤顺序执行，任一步失败时按严格逆序（LIFO）补偿已执行步骤。
   事务 id 幂等（并发/重复 `run` 共享同一次执行）；每个步骤至多补偿一次；
   补偿失败可重试，且单个补偿失败不会中断其余步骤的补偿（防止资源泄漏）；
   `assertNoLeaks()` 严格断言所有事务到达终态且无未补偿步骤。

## 运行要求

- Node.js >= 23.6（原生 TypeScript 类型剥离，无需编译、无需任何依赖）
- 或 Node >= 22.6 并加 `--experimental-strip-types`

## 运行

```sh
node --test test/      # 全部测试（node:test 内置运行器）
node examples/demo.ts  # 运行样例
```

## 状态机

```
PENDING -> RUNNING -> COMPLETED
                  \-> COMPENSATING -> COMPENSATED
                                   \-> COMPENSATION_FAILED   (assertNoLeaks 会报告)
```

每个步骤日志记录：`EXECUTED -> COMPENSATING -> COMPENSATED | COMPENSATION_FAILED`，
并携带幂等键 `txId#index:stepName` 与执行/补偿尝试次数。
