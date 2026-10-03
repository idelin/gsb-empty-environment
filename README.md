# protobuf-wire-codec (TypeScript)

零外部依赖的 Protocol Buffers 二进制 Wire-format 编解码器 + 反射模式校验引擎，
面向低延迟场景：解码热路径基于对象池与内存复用，稳态下零消息/数组分配，消除 GC 停顿。

## 要求

Node.js >= 22.6（利用原生 TypeScript 类型擦除运行，无需编译、无任何依赖）。

## 运行

```bash
npm test          # node --test test/*.test.ts
npm run example   # node examples/demo.ts
```

## 组件

- `src/wire.ts` — 手工解析的 Wire-format 原语：
  - Varint 7 位压缩（number 快路径 + bigint 全 64 位路径，10 字节上限校验）
  - ZigZag 有符号整数转换（32/64 位）
  - Fixed32/Fixed64 小端读写
  - Length-delimited 嵌套消息：通过可复用 reader 内的预分配 limit 栈移动游标，
    不拷贝字节、不分配子缓冲区
  - 所有读取均做边界检查，截断输入抛出 `TruncatedError`，非法结构抛出 `DecodeError`
- `src/pool.ts` — `ObjectPool<T>`：容量有界的空闲栈，双重释放检测，
  outstanding 计数支撑 `assertNoLeaks()` 严格泄漏断言
- `src/decoder.ts` — 反射模式校验引擎：`defineSchema` 定义结构（字段号/类型/
  repeated/packed/嵌套 schema，定义期即校验），`MessageDecoder` 按 schema 解码；
  消息对象按 schema 池化、repeated 数组统一池化，解码失败时递归回收已分配对象
- `src/encoder.ts` — schema 驱动编码器（用于往返测试与样例）

## 零分配设计

- 单个 `WireReader` 跨解码复用；嵌套消息只移动 limit 游标（`pushLimit`/`popLimit`）
- 解码出的消息对象来自按 schema 划分的对象池，repeated 字段数组来自共享数组池
- 调用方持有解码结果，用毕 `decoder.release(msg)` 递归归还整棵对象树
- 稳态基准（见 `npm run example`）：100 万次 decode+release 仅创建 1 个消息对象
  与 2 个数组，之后全部复用

## 验收边界覆盖（test/）

- `test/wire.test.ts` — varint 7 位边界值与规范编码向量、10 字节上限、ZigZag
  规范向量（含 ±2^63）、Fixed32/64 小端布局、嵌套 limit、skipField、各类截断
- `test/decoder.test.ts` — 多层嵌套消息、Packed Repeated（packed 与逐元素两种
  编码都接受）、未知字段跳过、逐字节截断前缀全部安全报错、恶意超长 length
  前缀防护、嵌套解码失败后部分对象全回收、池化对象复用断言、稳态池大小有界
- `test/pool.test.ts` — 获取/回收/重置、容量上界丢弃、零容量、双重释放抛错、
  泄漏检测、统计计数、100 万次循环无泄漏
- `test/concurrency.test.ts` — 64 个异步任务交错共享解码器、并发负载下解码
  错误不泄漏、4 个 worker_threads 真实线程并行解码且各自无泄漏
