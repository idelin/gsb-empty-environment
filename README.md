# negation-stream-joiner

带否定（取消）事件的双流时序 Join 连接器，TypeScript 实现，零依赖，使用 Node.js 内置测试运行器验证。

## 语义

- 左流事件 `L` 到达后等待 **5s** 窗口（`windowMs` 可配）内的右流事件 `R`，匹配条件 `R.ts <= L.ts + windowMs`（含边界），命中立即输出 `matched`。
- 超时无匹配 → 输出 `left-only`（孤立左结果）。
- 超时前收到取消事件 `cancel(key)` → 放弃等待，**不产生任何输出**，并清除该 key 的全部残余状态。
- 迟到的 `R`（`L` 已超时输出、但 `R.ts` 仍落在窗口内）→ **补配**：先输出 `retract-left-only` 撤回，再输出 `matched { late: true }`。
- 乱序的 `R` 先于 `L` 到达 → 缓冲等待，`L` 到达即匹配；缓冲超过 `allowedLatenessMs` 过期丢弃。
- 事件时间驱动：`advanceTime(t)` 推进水位线，定时器确定性触发；所有内部状态（pending / buffered / timedOut）均有过期边界，最终清零、零泄漏。

## 运行

需要 Node.js ≥ 22.6（利用原生 TypeScript 类型擦除，无需编译；仓库 `.nvmrc` 指定 23.9.0）。

```bash
nvm use          # 可选，切换到 .nvmrc 指定版本
npm test         # node --test 运行全部测试
npm run demo     # 运行验收场景样例
```

## API

```ts
import { TemporalJoiner } from './src/temporal-joiner.ts';

const joiner = new TemporalJoiner({ windowMs: 5000, allowedLatenessMs: 5000 });
joiner.left({ key: '1', ts: 0 });      // 左流事件
joiner.right({ key: '1', ts: 3000 });  // 右流事件
joiner.cancel('2');                    // 否定/取消事件
joiner.advanceTime(15_000);            // 推进事件时间，触发超时
joiner.drain();                        // 取出输出（也可传 onOutput 回调）
joiner.stats();                        // 状态与计数（用于泄漏检查）
```

## 项目结构

- `src/temporal-joiner.ts` — 核心组件
- `test/temporal-joiner.test.ts` — node:test 验收与边界测试
- `examples/demo.ts` — 验收场景运行样例
