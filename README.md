# 本地指标账本（metric-ledger）

季度考核权重可追溯调整：同一页面同时回答“当时看到多少分”和“按现在规则重算是多少”。

## 设计要点

- **精确分数**：`src/fraction.ts` 的 `Frac` 用 bigint 保存约分后的分子/分母，
  权重与得分全程整数分数运算，拒绝浮点输入，不靠误差容忍。
- **修订与撤销**：每条修订含业务有效半开区间 `[from, to)`、全局唯一录入序号、
  规则 ID 与一整套权重（非负且精确和为 1）。同一天取“查询可见序号以内、
  未被可见撤销命中”的最新整套权重。撤销也是一条录入，只在撤销序号之后
  （含）的查询视角生效，随后回退到仍有效的前一版。
- **缺分不当 0**：每日得分由独立事件给出，缺分日期在报表中列为缺失项
  （含缺失指标），季度总分只对完整天取算术平均。
- **双时点查询**：`compareQuarter(from, to, thenSeq)` 一次返回
  `then`（当时视角）与 `now`（当前视角）两份报表，均含规则版本分段
  `segments`（受修订影响的日期段）。
- **缓存只失效受影响区间**：当前视角逐日缓存；新增修订只失效其业务区间，
  撤销只失效被撤销修订的区间，补录只失效对应日期。历史 asOf 查询不污染缓存。
- **增量对照**：`scanQuarter` 完全绕过缓存独立逐日扫描，测试用它逐字段
  对照增量（缓存）路径的结果。

## 运行测试

需要 Node >= 22.18 或 >= 23.6（原生 TypeScript 类型擦除 + 内置 `node:test`）：

```sh
npm test          # 等价于 node --test
```

若系统默认 Node 版本过低（如 v16），先切换再跑：

```sh
nvm use 23 && npm test
```

## API 速览

```ts
import { Ledger } from "./src/ledger.ts";

const ledger = new Ledger();
ledger.addRevision({ seq: 1, ruleId: "R1", from: "2024-01-01", to: "2024-04-01",
                     weights: { a: "1/2", b: "1/2" } });
ledger.addScores([{ date: "2024-01-01", metric: "a", score: "1" }, ...]); // 批量补录
ledger.addRevision({ seq: 2, ruleId: "R2", from: "2024-01-01", to: "2024-04-01",
                     weights: { a: "1/4", b: "3/4" } }); // 追溯到季初
ledger.retract(3, 2); // 撤销 seq=2，只在序号 3 之后的视角生效

const { then, now } = ledger.compareQuarter("2024-01-01", "2024-04-01", 1);
// then.total / now.total 为 Frac；then.segments / now.segments 为规则版本日期段
```

说明：得分事件本身不带录入序号，历史 asOf 查询使用当前已知的全部得分；
双时点语义针对的是规则（权重）版本的可见性。
