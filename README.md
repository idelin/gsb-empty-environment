# state-history-tree

TypeScript 状态历史库：支持回到旧版本**只读查看**而不移动真实最新头，查看期间新事件继续追加到头部；从旧版本发起编辑会**显式创建分支**，永远不会覆盖 `main` 或任何已有分支。底层是一棵不可变的**持久版本树**，可序列化快照并恢复。

## 核心概念

- `VersionTree<S>`：持久（不可变）版本树。每次追加/建分支都返回新树，旧引用永远有效；分支只是指向节点的命名指针。`toJSON()` / `fromJSON()` 支持持久化。
- `HistoryStore<S>`：在版本树之上维护两个游标——
  - **head**：活动分支的真实最新头，只有 `dispatch` / `editFrom` 能推动它；
  - **view**：用户当前查看的版本，`checkout` 只移动它，绝不动 head。

## 行为规则

| 操作 | 效果 |
| --- | --- |
| `dispatch(event, state)` | 追加到活动分支真实头部；视图在头部时跟随前进，脱离头部查看旧版时保持不动 |
| `checkout(versionId)` | 纯查看导航，head 不变；查看期间 `dispatch` 的新事件照常入头 |
| `backToHead()` | 视图回到真实头部 |
| `editFrom(versionId, …)` | `versionId` 是头部时等同 `dispatch`；否则显式创建新分支并切换过去，源分支（含 `main`）不被覆盖；分支名已存在则抛错 |
| `switchBranch(name)` | 切换活动分支，视图重置为该分支头部 |
| `watchView(resource)` | 视图作用域资源：视图移走时先跑 cleanup 再为新视图 setup；`release()` 时跑最后一次 cleanup |

## 示例

```ts
import { HistoryStore } from "state-history-tree";

const store = HistoryStore.create({ initialState: 0 });
store.dispatch("add", 1); // v2
store.dispatch("add", 2); // v3，head = v3

store.checkout("v1");       // 只读查看旧版本，head 仍是 v3
store.dispatch("add", 3);   // 新事件继续入头：head = v4，视图仍停在 v1

// 从旧版本编辑 → 显式创建分支，main 不受影响
store.editFrom("v1", "alt", 100, "experiment");
store.activeBranch; // "experiment"
store.versionTree.getBranchHeadId("main"); // 仍指向 v4

// 视图作用域资源，切换/查看时自动清理
const release = store.watchView((view) => {
  const sub = subscribeToRemote(view.versionId);
  return () => sub.close(); // 视图移走时自动调用
});

// 持久化与恢复
const snapshot = JSON.stringify(store.snapshot());
const restored = HistoryStore.restore(JSON.parse(snapshot));
```

## 开发

```sh
npm test          # 零依赖行为测试（Node，直接跑 dist/）
npm run build     # 需要 typescript 依赖时重新编译 src -> dist
```

`dist/` 是 `src/` 的等效编译产物（当前环境离线无 tsc，为手工转译并已由测试验证；有网络时 `npm i && npm run build` 可重新生成）。
