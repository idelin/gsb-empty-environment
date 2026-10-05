# trace-diff 轨迹差分工具

比较两个执行器（executor）产生的轨迹，按输入给定的**可观察动作**定义做语义对齐，
定位**首次语义分歧**，并还原分歧点附近的**内部步骤窗口**。

## 特性

- **内部步骤透明**：两侧轨迹允许含有不同数量、无副作用的内部步骤（未列入
  `observable` 的动作），对齐时自动跳过，只比较可观察动作序列。
- **循环压缩段**：轨迹可以以 `loop` 段（body × times）形式给出。比较过程基于
  游标流式推进，**不做整体展开**；纯内部循环段会 O(1) 快进，`times` 为百万级也
  不会展开。
- **三类分歧输出**：
  - `value-mismatch`：同一可观察动作的参数/返回值/状态不同（带 JSON 路径）。
  - `missing-action`：一侧轨迹提前结束，报告另一侧缺失的动作及剩余数量。
  - `order-mismatch`：同一位置动作不同，通过有界前瞻区分"乱序"与"多出/缺失"。
- **内部窗口还原**：分歧发生时，输出两侧自上一个匹配可观察动作以来的内部步骤
  （保留最近 `maxWindow` 条，超出标记 `truncated`），以及分歧动作本身。

## 输入格式（JSON）

```json
{
  "options": {
    "observable": ["navigate", "click", "read"],
    "ignoreKeys": ["ts"],
    "numericTolerance": 0,
    "maxWindow": 20,
    "lookahead": 10,
    "maxValueDiffs": 50
  },
  "left":  "<trace>",
  "right": "<trace>"
}
```

`<trace>` 支持四种形态：

```
[event, ...]                        // 纯事件数组
[segment, ...]                      // 段数组（run / loop，不可混排）
{ "events": [event, ...] }
{ "segments": [segment, ...] }
```

- 事件：`{ "action": "click", "args": {...}, "result": ..., "status": "ok"|"error", "error": "...", "seq": 0 }`
- 段：`{ "kind": "run", "events": [...] }` 或 `{ "kind": "loop", "body": [...], "times": N }`

选项说明：

| 选项 | 默认 | 含义 |
| --- | --- | --- |
| `observable` | （必填） | 可观察动作名列表，其余动作视为无副作用内部步骤 |
| `ignoreKeys` | `[]` | 值比较时忽略的键名（任意深度，如时间戳） |
| `numericTolerance` | `0` | 数值比较的绝对容差 |
| `maxWindow` | `20` | 每侧内部窗口最多保留的条数 |
| `lookahead` | `10` | 顺序分歧时的前瞻可观察动作数 |
| `maxValueDiffs` | `50` | 单次分歧最多报告的值差异条数 |

## 使用

```bash
# CLI（文件参数或 stdin），退出码：0 一致 / 1 分歧 / 2 输入错误
node .build/src/cli.js examples/divergent.json
cat examples/divergent.json | node .build/src/cli.js

# 测试（离线可跑：内置零依赖转译器将 src/test 的 TS 子集转译到 .build/ 后执行）
npm test

# 标准构建（需要网络安装 devDependencies 后使用 tsc）
npm run build
```

API：

```ts
import { diffTrajectories } from './src/index.js';

const report = diffTrajectories(left, right, { observable: ['navigate', 'click'] });
```

## 输出报告

```
{
  "equal": false,
  "matchedObservables": 3,          // 分歧前已匹配的可观察动作数
  "divergence": {
    "kind": "value-mismatch",        // 或 missing-action / order-mismatch
    "observableIndex": 3,            // 首次分歧的可观察动作序号（0 起）
    "left":  "<分歧处左侧事件或 null>",
    "right": "<分歧处右侧事件或 null>",
    "valueDiffs": [{ "path": "$.result", "left": "...", "right": "..." }],
    "remainingObservables": null,    // missing-action 时：另一侧剩余可观察动作数
    "foundInLeftAt": null,           // order-mismatch 时：对方动作在本侧前瞻中的偏移
    "foundInRightAt": null,
    "detail": "人类可读的说明",
    "window": {
      "left":  { "lastMatchedObservable": "...", "internal": ["..."],
                 "internalTotal": 1, "truncated": false, "divergingObservable": "..." },
      "right": "同上"
    }
  },
  "stats": {
    "left":  { "totalEvents": 7, "consumedEvents": 6, "internalEvents": 3, "matchedObservables": 3 },
    "right": "同上"
  }
}
```

## 比较语义与限制

- 值比较作用于 `{ args, result, status, error }` 视图；`seq`/`meta` 仅作定位信息不参与比较。
- 对象键无序、数组有序；缺失键以 `null` 呈现在差异中（`undefined` 归一为 `null`）。
- 只报告**首个**分歧；分歧后不再继续对齐。
- 顺序分歧的前瞻是有界的（`lookahead`），超出范围按"未找到"报告。

## 代码结构

- `src/types.ts` — 输入/输出类型定义
- `src/compare.ts` — 深度值比较（路径、忽略键、数值容差）
- `src/cursor.ts` — 段游标：惰性流式读取 run/loop 段，纯内部循环 O(1) 快进
- `src/diff.ts` — 对齐引擎与分歧报告
- `src/cli.ts` — 命令行入口
- `test/diff.test.ts` — 单元测试（19 例）
- `scripts/strip-types.mjs` / `scripts/run-tests.mjs` — 离线转译与测试脚本
