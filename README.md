# allen-temporal-scheduler

时区与日历排程的时间区间代数约束求解器：基于 **Allen 区间代数** 的时态关系约束网络，
结合 **STN（Simple Temporal Network）** 度量层，传播 `Before`、`Meets`、`Overlaps` 等
时态约束，通过传递闭包做相容性检查，输出所有任务的最早/最晚可行时间窗与项目关键路径窗口。

## 架构

| 模块 | 职责 |
| --- | --- |
| `src/allen.ts` | 13 个基本关系（b/m/o/s/d/f/eq 及逆关系）、逆运算、**运行时枚举生成的合成表** |
| `src/network.ts` | 定性约束网络；Allen 路径相容性算法（传递闭包式不动点传播） |
| `src/stn.ts` | 度量层 STN：`x - y <= c` 约束，Floyd-Warshall 闭包 + 负环检测 |
| `src/solver.ts` | `Scheduler`：两层协同；定性边经**凸包**（所有析取支共同蕴含的端点界）注入 STN |

设计要点：

- **合成表不手抄**：`compose(R, S)` 通过在小型线性序上穷举三区间的端点配置生成，
  表中每一项都是可观测到的关系，天然正确（见 `test/solver.test.ts` 对经典结果的校验）。
- **严格不等式**按整数天粒度建模为单位间隙（`STRICT_GAP = 1`），如 `overlaps` 要求
  `B.start < C.start` 即 `C.start >= B.start + 1`。
- **析取约束**（如 `{before, meets}`）在度量层取其凸包：只保留每个析取支都成立的界，
  因此是安全的松弛。

## 运行

要求 Node.js >= 23.6（原生 TypeScript 类型擦除运行；22.6+ 可加
`--experimental-strip-types`）。仓库根目录附有 `.nvmrc`。

```bash
npm test        # Node 内置测试运行器（node:test）
npm run example # 验收场景示例
```

## 验收场景（examples/schedule.ts）

约束：任务 A（2 天）必须在 B（3 天）之前结束；B 与 C（4 天）部分重叠；
C 必须在工期第 5 天前开始。

实际运行输出：

```
=== 时态约束传播（Allen 区间代数，传递闭包后） ===
  A -> B: {before}
  B -> C: {overlaps}
  A -> C: {before}        ← b ∘ o = b，传递闭包推出的间接关系

=== 任务可行时间窗（单位：天，项目自第 0 天开始） ===
  任务  开始窗口        结束窗口        总时差
  A     [0, 1]          [2, 3]          1
  B     [2, 3]          [5, 6]          1
  C     [3, 4]          [7, 8]          1

任务 A 的最迟结束时间: 第 3 天
项目全局关键路径窗口: [7, 8] 天
关键路径上的任务: A -> B -> C
```

推导链：`C.start <= 4`（第 5 天前）→ `B.start <= 3`（overlaps 要求 B 先开始）
→ `A.end <= 3`（before 即 finish-to-start），故 A 最迟第 3 天结束；
项目完工窗口由关键链 A→B→C 决定为 `[7, 8]` 天。

## 测试结果

`node --test "test/*.test.ts"`：**13 个测试全部通过**，覆盖

- 13 个基本关系的识别、逆运算对合性；
- 合成表与经典 Allen 结果一致（`b∘b={b}`、`o∘o={b,m,o}`、`b∘bi=全集` 等）；
- 路径相容性经传递闭包收紧间接关系（A before B ∧ B overlaps C ⇒ A before C）；
- 定性层循环矛盾检测、度量层截止时间冲突检测；
- 验收场景的全部窗口数值、A 的最迟结束时间、项目关键路径窗口；
- 析取约束凸包、可变工期（`{min, max}`）下的浮动窗口。

```
ℹ tests 13
ℹ pass 13
ℹ fail 0
```

## API 速览

```ts
import { Scheduler } from './src/solver.ts';

const s = new Scheduler();
s.addTask('A', 2);                    // 固定工期
s.addTask('B', { min: 2, max: 4 });   // 可变工期
s.addRelation('A', 'before', 'B');          // 单个关系
s.addRelation('A', ['before', 'meets'], 'B'); // 析取关系
s.startBefore('C', 5);                // C.start < 5（严格）
s.startOnOrBefore('C', 5);
s.endOnOrBefore('C', 9);
s.releaseOn('A', 0);

const r = s.solve();
// r.consistent / r.qualitativelyConsistent
// r.tasks[i]: { earliestStart, latestStart, earliestEnd, latestEnd, float }
// r.projectWindow: [最早完工, 最晚完工]
// r.criticalPath: 时差最小的任务链
// r.relationBetween('A', 'C'): 传播后的定性关系集
```
