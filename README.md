# layered-settings

三层设置库：`default`（默认）< `project`（项目）< `session`（会话），高层优先。

## 语义

- **显式 `null` 是值**：`null` 作为叶子参与合并，会遮蔽下层的值（包括下层的整棵子树）；要撤销覆盖请用 `unset`。
- **`unset` 只撤当前层**：删除指定层在该路径的覆盖后，下层的值重新显现（继承）。
- **对象按叶合并**：各层在同一路径下的对象逐键递归合并，每个叶子取自定义它的最高层。
- **数组整体替换**：数组是叶子，高层数组完整遮蔽低层数组，不逐元素合并。
- **遮蔽是折叠语义**：中间层的叶子（含 `null`）遮蔽其下所有层的整棵子树；更高层的对象只贡献自己的叶子，不会"复活"被遮蔽的下层子树。
- **事务统一发布**：`update` / `transaction` 中的多条路径变更作为一个批次应用，订阅者只收到一次通知，且永远看不到中间态。

## API

```ts
import { SettingsStore } from './src/settings.ts';

const store = new SettingsStore({ default: { editor: { fontSize: 12, tabSize: 4 } } });

store.set('project', 'editor.tabSize', 2);        // 路径支持点分字符串或段数组
store.set('session', 'editor.wordWrap', null);    // null 是显式值
store.get('editor');                              // { fontSize: 12, tabSize: 2, wordWrap: null }
store.sourceOf('editor.tabSize');                 // 'project'
store.resolve('editor');                          // { value, source, leaves } — 最终值 + 逐叶来源
store.unset('session', 'editor.wordWrap');        // 只撤 session 层

store.update([                                   // 事务：整批应用，只发布一次
  { layer: 'session', path: 'a.b', op: 'set', value: 1 },
  { layer: 'project', path: 'c', op: 'unset' },
]);
store.transaction((tx) => { tx.set(...); tx.unset(...); });

const off = store.subscribe((event) => {
  event.changes;      // 本批次全部变更
  event.resolution;   // 批次后的根解析结果（最终值 + 来源）
});
```

`resolve(path)` 返回：

- `value`：合并后的最终值（深拷贝）；路径未定义时为 `undefined`。
- `source`：最终值来自唯一一层时为该层名；对象由多层叶子合并而成时为 `undefined`。
- `leaves`：逐叶来源 `[{ path, value, source }]`，`null` 与数组都是叶子。

## 运行测试

需要 Node ≥ 22.18（原生 TypeScript 类型擦除）：

```sh
node --test test/settings.test.ts
```

测试包含一个独立的逐层参考模型（扁平化叶子表 + 前缀遮蔽合并），用确定性随机操作序列
（30 轮 × 200 个事务批次）对库的最终值树、逐叶来源与单点查询做全量核对。
