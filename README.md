# suggestion-merge

一个 TypeScript 建议聚合库：合并多个异步来源的候选，按规范键去重，输出全量排序的建议列表。

## 特性

- **规范键去重**：候选键经 NFKC、去空白、小写化、折叠内部空白后归并（可注入自定义 `normalize`）。
- **得分取最大**：同一规范键的胜出分为所有来源的最大分。
- **说明保留来源集合**：每条建议保留贡献来源 id 集合（`sources`）及逐来源说明（`explanations`）。
- **来源撤回重选赢家**：`retractSource(id)` 撤销该来源全部贡献，受影响键的赢家从剩余来源中重新选出。
- **高亮按身份保留**：高亮跟踪规范键而非列表下标，重排序与撤回后仍指向同一项；键暂时消失时记住它，同代内重现即恢复；未变化的键在多次读取间保持对象引用不变。
- **查询代际隔离**：`beginQuery` 返回单调递增的代际令牌，迟到批次携带过期令牌会被 `applyBatch` 拒绝，乱序/跨代异步返回不会污染当前查询。

## 用法

```ts
import { SuggestionEngine } from 'suggestion-merge';

const engine = new SuggestionEngine();
const gen = engine.beginQuery('hel');

// 各来源异步返回，顺序任意；过期代际返回 false
engine.applyBatch(gen, 'history', [{ id: 'h1', key: 'Hello', score: 0.9, explanation: 'seen 12 times' }]);
engine.applyBatch(gen, 'prefix',  [{ id: 'p1', key: ' hello ', score: 0.6 }]);

engine.suggestions();
// => [{ key: 'hello', score: 0.9, sources: Set{'history','prefix'}, explanations: Map{...} }]

engine.highlight('hello');      // 按身份跟踪
engine.retractSource('history');// 重新选赢家：hello 跌至 0.6，高亮仍在 hello 上
```

## 测试

需要 Node ≥ 23.6（或 ≥ 22.6 加 `--experimental-strip-types`）直接运行 TypeScript：

```sh
npm test
```

测试以独立的全量聚合参考实现交叉核对乱序异步返回与查询代际切换。可选类型检查：`npm i -D typescript @types/node && npm run typecheck`。
