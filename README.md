# focus-traversal

TypeScript 工具：为虚拟节点树计算焦点遍历顺序。

## 规则

- **正 tabindex**：先于一切，按值升序，值相同按文档序。
- **零 tabindex / 原生可聚焦**（`focusable: true`）：跟在正值之后，按文档序。
- **负 tabindex**：仅可程序聚焦，不进入 Tab 序列。
- **隐藏祖先**（`hidden: true`）：整个子树不可聚焦（含程序聚焦）。
- **禁用组**（`group: true` + `disabled: true`）：整个子树不可聚焦，
  但 `exempt` 列出的子树显式豁免（类似 `fieldset[disabled]` 中的 `legend`）。
  外层组的豁免不会穿透内层禁用组。
- **重复 id**：构造、插入、重命名时一律拒绝（`DuplicateIdError`）。

## 用法

```ts
import { FocusTree } from './src/index.ts';

const tree = FocusTree.fromSpec({
  id: 'root',
  children: [
    { id: 'username', tabindex: 2 },
    { id: 'password', tabindex: 1 },
    { id: 'submit', focusable: true },
    { id: 'modal', tabindex: -1 },
  ],
});

tree.tabOrder();           // ['password', 'username', 'submit']
tree.programmaticOrder();  // ['modal']

tree.update('modal', { tabindex: 0 });   // 动态属性更新
tree.insert('root', { id: 'cancel', focusable: true });
tree.snapshot();           // 全树展开快照，可重建出完全一致的结果
```

所有查询都通过全树展开（`expandTree`）现算，因此任意序列的动态更新
结果与从当前 spec 全新构建的树完全一致（测试中以 fuzz 方式核对）。

## 运行测试

需要 Node.js >= 23.6（原生 TypeScript 类型擦除）：

```sh
node --test
```
