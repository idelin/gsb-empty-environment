# tournament-ranking

赛事排名库（TypeScript，零依赖）：积分相同先比较组内相互战绩，拆出部分名次后对剩余子组
递归重新决胜，最终仍同分则保留并列，并输出完整的解释树。

## 为什么不能用一次性排序比较器

- 相互战绩关系**不成传递**：A 胜 B、B 胜 C、C 胜 A 时，任何比较器都会给出依赖输入
  顺序的结果（甚至违反排序算法的契约）。
- 拆出部分名次后，剩余子组必须**只用子组内部的比赛重新计算相互战绩**，这是分组
  递归而非一次比较能表达的。

## 算法

1. 按当前范围的积分分桶（顶层为总积分；子组为组内相互战绩积分）；
2. 独占一个积分档的队伍直接确定名次；
3. 同分且只占原组一部分的子组，递归进入下一轮相互战绩决胜；
4. 同分且覆盖整个当前组（无法再拆分）时，保留并列。

名次采用标准竞赛排名（如 1、2、2、4）。

## 用法

```ts
import { rankTeams, formatStandings, formatExplanation } from './src/index.ts';

const result = rankTeams(
  ['A', 'B', 'C'],
  [
    { type: 'score', home: 'A', away: 'B', homeScore: 1, awayScore: 0 },
    { type: 'score', home: 'B', away: 'C', homeScore: 1, awayScore: 0 },
    { type: 'score', home: 'C', away: 'A', homeScore: 1, awayScore: 0 },
    // 弃权：{ type: 'forfeit', home: 'X', away: 'Y', forfeitedBy: 'home' }
  ],
  // 可选自定义积分，默认 FIBA 风格：胜 2 / 负 1 / 平 1 / 弃权胜 2 / 弃权负 0
  // { win: 3, draw: 1, loss: 0, forfeitWin: 3, forfeitLoss: 0 }
);

console.log(formatStandings(result));
console.log(formatExplanation(result.explanation));
```

`rankTeams` 返回：

- `standings`：完整排名，并列队伍共享一个条目（`teams` 长度 > 1）；
- `explanation`：解释树，每个节点记录比较范围（总积分/相互战绩）、各队积分，
  叶子为「确定名次」或「并列」。

## 测试

```sh
npm test          # node --test 'tests/*.test.ts'（Node >= 23.6 原生运行 TS）
npm run typecheck # 需要本地安装 typescript
```

覆盖场景：三队循环胜负（结果与输入顺序无关）、弃权（弃权方 0 分、不战而胜计入
相互战绩）、多层拆组（拆出部分名次后剩余子组重新决胜，直至完全分出名次或保留并列）。
