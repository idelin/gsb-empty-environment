# exact-leaderboard

TypeScript 排行榜库：可撤销的积分事件、精确整数总分、top-k 变更输出，并用全历史重放核对增量状态。

## 事件语义

- `score` 事件：`{ type: 'score', id, userId, delta }`，`id` 全局唯一，`delta` 为精确整数（`bigint` / 整数 `number` / 整数字符串，内部统一为 `bigint`，可安全超过 2^53）。
- `revoke` 事件：`{ type: 'revoke', targetId }`，按事件 id 撤销：
  - 目标已知：立即移除其分数贡献；
  - 目标未知：撤销先挂起（pending），原事件之后到达也不生效（stillborn）；
  - 重复撤销：幂等 no-op。
- 重复的 `score` 事件 id 至多生效一次。

## 排名规则

- 总分降序；并列时用户 id 升序（默认按码元序，可用 `compareUserIds` 自定义）。
- 总分为 0 的用户仍保留在榜单上（0 是合法分数，可跨零排名）。
- 每次 `apply` 返回 top-k diff：`added`（新进入）、`removed`（跌出，带原名次）、`moved`（名次变化，含 from/to）。

## 用法

```ts
import { Leaderboard } from './src/leaderboard.ts';

const board = new Leaderboard({ k: 3 });

board.apply({ type: 'score', id: 'e1', userId: 'alice', delta: 10 });
board.apply({ type: 'revoke', targetId: 'e9' });        // 未知 id：挂起
board.apply({ type: 'score', id: 'e9', userId: 'bob', delta: 99 }); // 不生效

console.log(board.topK);            // [ { rank: 1, userId: 'alice', score: 10n } ]
board.verifyAgainstReplay();        // 全历史重放核对，状态不一致则抛错
```

## 测试

需要 Node.js >= 22.6（原生 TypeScript 类型擦除）：

```sh
npm test
```

覆盖：撤销先到、重复撤销、重复事件 id、跨零分排名、超过 2^53 的精确整数，
以及随机模糊测试——每个事件后都用全历史重放（`computeTotalsFromHistory` + `rankTotals`）
核对增量维护的总分与 top-k。
