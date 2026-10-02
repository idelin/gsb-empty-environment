# proto-wire-adaptive

零外部依赖的 Protocol Buffers 二进制 Wire-format 解析器 + 反射模式校验引擎 +
自适应动态阈值伸缩与负载感知限流器（TypeScript / Node.js 内置测试）。

## 组件

- `src/wire.ts` — Wire-format 核心编解码
  - Varint 7 位压缩编码（bigint 全程 64 位精度，负数按 int64 补码）
  - ZigZag 有符号整数转换
  - Fixed32 / Fixed64 小端序
  - Length-delimited 负载与 tag 解析
  - `ByteReader` 全程边界检查，截断时抛 `TruncatedFieldError`（携带字段号、
    期望/剩余字节数），绝不越界读
- `src/message.ts` — 反射模式校验引擎
  - Schema 描述驱动的 `encodeMessage` / `decodeMessage`
  - 多层嵌套 Length-delimited 消息、Packed / 非 Packed Repeated 数组
  - 未知字段安全跳过、wire type 校验、嵌套深度与消息大小上限
- `src/adaptive.ts` — 自适应阈值与负载感知
  - `AdaptiveThreshold`：基于 CPU/内存/错误率/队列深度综合压力评分，
    AIMD（加性扩张、乘性收缩）+ 滞回区间 + 冷却期，规避振荡与雪崩；
    历史记录有界，无内存泄漏
  - `LoadAwareLimiter`：动态阈值为上限的异步信号量，支持队列满快速失败
    （`ShedLoadError` 负载卸载）、`AbortSignal` 取消、`assertDrained()` 泄漏断言

## 运行

要求 Node.js >= 23.6（内置 TypeScript 类型擦除，零构建、零依赖）。

```bash
npm test    # node --test（37 个用例）
npm run demo
```

## 测试结果

```
ℹ tests 37
ℹ pass 37
ℹ fail 0
```

覆盖：varint/zigzag/fixed 边界值与截断防护、多层嵌套与 packed repeated 往返、
未知字段跳过、深度/大小上限、阈值伸缩（收缩钳制、扩张钳制、滞回、冷却）、
并发上限严格遵守、动态扩限唤醒、负载卸载、Abort 取消、异常路径槽位释放、
500 并发风暴后状态归零无泄漏。
