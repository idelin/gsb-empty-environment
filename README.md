# tournament-ranking

赛事排名库（TypeScript）。核心规则：

1. 先按总积分分组；
2. 同分组内依次应用决胜条件链（默认：相互战绩积分 → 相互战绩净胜球 →
   相互战绩进球数 → 总净胜球 → 总进球数）；
3. 某条件把同分组**拆出部分名次后，对剩余子组从头重新应用整条条件链**
   （子组的相互战绩只统计子组内部比赛，结果可能与之前不同）；
4. 条件链用尽仍相同的队伍**保留并列**，共享同一名次，后续名次跳号（1、1、3）。

## 为什么不能用一次排序比较器

相互战绩比较不是传递关系：A 胜 B、B 胜 C、C 胜 A 时，两两比较的结果取决于
当前子组里有哪些队（拆走 D 后，剩余队伍的相互战绩要重算）。`Array.sort`
的比较器必须满足传递性，用「两两比较」草率排序会得到依赖排序算法内部
顺序的错误结果。正确做法是本库采用的**递归分组**：每层按某一条件把同分
组拆成有序子组，再对每个子组递归重决胜，而不是对整表做一次比较器排序。

## 用法

```ts
import { rankTeams, formatExplanation } from './src/index.js';

const result = rankTeams(
  ['A', 'B', 'C'],
  [
    { home: 'A', away: 'B', homeScore: 3, awayScore: 0 },
    { home: 'B', away: 'C', homeScore: 1, awayScore: 0 },
    // C 弃权：按 forfeitScore（默认 3:0）判负，计入积分与相互战绩
    { home: 'C', away: 'A', homeScore: 0, awayScore: 0, forfeitedBy: 'C' },
  ],
  // 可选：scoring / forfeitScore / criteria（自定义条件链）
);

result.entries;    // [{ rank, teams, tied }]，并列时 teams 多于一个
result.standings;  // 按名次排序的积分表（含 forfeits 计数）
result.explanation; // 解释树：每层记录所用条件、各队得分、无法区分的条件
console.log(formatExplanation(result.explanation));
```

解释树节点类型：`points`（按总积分分组）→ `criterion`（某条件拆组，
`tiedCriteria` 记录此前无法区分的条件）→ 叶子为 `team`（确定名次）或
`tie`（条件用尽，保留并列）。

## 测试

```sh
npm test
```

覆盖：三队循环胜负（完全相同 → 并列；净胜球不同 → 分名次）、弃权判负与
可配置弃权比分、多层拆组（先拆出部分名次再对剩余子组重新决胜）、并列
名次跳号、解释树格式化。

## 环境说明

本仓库源码是标准 TypeScript（`src/`、`tests/`）。当前离线环境没有
`tsc`/`tsx`，因此 `npm test` 通过 `scripts/strip-types.mjs`（一个面向本
仓库受限 TS 子集的类型擦除器）把源码转换到 `.build/` 后用 Node 直接运
行测试。源码约束见该文件头部注释。有网络时建议：

```sh
npm install
npm run typecheck   # tsc --noEmit，对 src/ 做严格类型检查
```
