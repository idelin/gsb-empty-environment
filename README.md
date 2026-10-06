# exact-leaderboard

事件溯源排行榜库(TypeScript):事件可撤销、分数为精确整数(`bigint`)、
按总分降序 + 用户 id 升序排名,并对前 k 名输出增删与位置变更 diff。

## 事件语义

- `score` 事件:`{ kind: "score", id, userId, delta }`,`delta` 为 `bigint`,累加到用户总分。
- `revoke` 事件:`{ kind: "revoke", id }`,按事件 id 撤销:
  - 撤销已知(生效中)事件:立即回滚其 `delta`。
  - 撤销未知事件:先挂起,原事件之后到达也**不会生效**。
  - 重复撤销 / 重复事件 id:幂等 no-op,`apply` 返回 `accepted: false`。

## 排名与 diff

- 排名:总分降序,并列时用户 id 升序(数值 id 按数值、字符串按字典序,可传自定义比较器)。
- 跨零分:零分与负分用户照常参与排名,撤销回零的用户不会被移除。
- `apply` 返回 `TopKDiff`:`entered`(新进前 k)、`left`(跌出前 k,记录旧名次)、
  `moved`(名次变化,`from` → `to`)。仅名次不变、分数变化的条目不出现在 diff 中。

## 使用

```ts
import { Leaderboard } from "exact-leaderboard";

const board = new Leaderboard<number>(100); // 前 100 名
const { accepted, diff } = board.apply({ kind: "score", id: "e1", userId: 7, delta: 9007199254740993n });
board.apply({ kind: "revoke", id: "e1" });
board.getTopK();   // 前 k 名(含名次)
board.snapshot();  // 完整排名
board.rankOf(7);   // 1-based 名次或 null
```

## 验证

`npm test` 编译并运行测试:单元测试覆盖撤销先到、重复撤销、跨零分排名、
2^53 以上精确整数;模糊测试对随机事件流(含大量未知/重复撤销与超大 delta)
逐步做三件事核对——增量结构 vs `Leaderboard.replay` 全历史重放 vs 独立朴素模型,
并用 diff 在影子副本上重建前 k 名。

构建:`npm run build`(需要本机可用的 `tsc`,TypeScript >= 4.9,target ES2020)。
