# negation-stream-joiner

双流时序 Join 组件（TypeScript）：左流等待右流匹配，支持否定（取消）事件、超时孤立输出与迟到补配。

## 语义

- 左流事件 `L` 到达后，在事件时间窗口 `[L.ts, L.ts + 5s]` 内等待右流事件 `R`。
- 窗口内 `R` 到达（含乱序提前到达）→ 立即输出 `matched`。
- 窗口内收到同 key 的取消事件 → 放弃等待，**不输出任何结果**。
- 水位线越过截止时间仍无匹配 → 输出 `left-only` 孤立结果。
- 迟到的 `R`（事件时间落在已超时 `L` 的窗口内）→ 输出 `late-matched` 补配修正。
- 时间模型为事件时间 + 水位线，不依赖墙钟，可确定性测试；水位线推进时自动回收过期状态（零泄漏）。

## 环境要求

Node.js >= 23.6（原生类型擦除，直接运行 `.ts`，无需编译）。

## 运行

```bash
npm test    # node:test 内置测试（6 个用例）
npm run demo  # 运行样例：模拟验收场景事件流
```

## 文件

- `src/joiner.ts` — 核心组件 `TemporalJoiner`
- `src/demo.ts` — 运行样例
- `test/joiner.test.ts` — 验收测试（node:test）
