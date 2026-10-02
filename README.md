# json-crdt-tree

分层 JSON CRDT（TypeScript）：Array / Map / Register 三种节点任意混合嵌套，
状态型 CvRDT 原地三向合并，保证并发修改不同层级属性时精准合并且不替换子树引用。

## 设计

| 组件 | 算法 | 说明 |
| --- | --- | --- |
| `LWWRegister` | Lamport 时钟 + 副本 ID 全序 | 原始值的最后写入者胜寄存器 |
| `MapCRDT` | OR-Map（键级墓碑） | 同 tag 递归合并；并发异 tag 按 LWW 裁决 |
| `ArrayCRDT` | RGA | 元素唯一 ID + 父链定序 + 墓碑删除，并发插入定序确定 |
| `JsonCRDT` | 文档层 | 路径寻址 `['list', 0, 'title']`，`merge()` 原地递归合并 |

所有 `merge()` 满足交换律 / 结合律 / 幂等律；已存在的节点对象一律就地更新，
绝不替换引用——持有子树引用的协作方在合并后引用依然有效。

## 环境要求

Node.js >= 22.6（利用内置 TypeScript 类型擦除直接运行 `.ts`，零依赖、零构建）。

## 运行

```sh
npm test          # Node.js 内置测试（node:test）
npm run example   # 运行样例
```

## 用法

```ts
import { JsonCRDT } from './src/document.ts';

const a = JsonCRDT.create('A', { list: [{ title: 'hello', tags: ['ts'] }] });
const b = a.fork('B');

a.set(['list', 0, 'title'], 'hello v2'); // A 改标题
b.push(['list', 0, 'tags'], 'go');       // B 推标签

const ref = a.nodeAt(['list', 0]);
a.merge(b);                              // 原地三向合并

a.get(['list', 0, 'title']);             // 'hello v2'
a.get(['list', 0, 'tags']);              // ['ts', 'go']
a.nodeAt(['list', 0]) === ref;           // true：列表项引用未被覆盖
```

## API

- `JsonCRDT.create(replica, initial)` / `doc.fork(replica)` — 创建 / 派生副本
- `doc.set(path, value)` — 设置（Register 原地更新，容器替换子树）
- `doc.push(path, value)` / `doc.insert(path, index, value)` — 数组追加 / 插入
- `doc.remove(path)` — 删除 Map 键或数组下标（墓碑语义）
- `doc.get(path)` / `doc.nodeAt(path)` / `doc.value()` — 读取
- `doc.merge(other)` — 原地合并，返回 `this`

## 测试输出

```
✔ 验收：并发修改 root.list[0].title 与 root.list[0].tags.push 精准合并，且不替换列表项引用
✔ 收敛：双向合并结果一致（交换律）且重复合并幂等
✔ 并发 push 同一数组：两个元素都保留且各副本定序一致
✔ 混合嵌套：Map/Array/Register 跨层级并发修改互不干扰
✔ 删除语义：Map 键删除与数组元素删除在合并后生效
✔ 并发写同一 Register：按 Lamport 时钟 LWW 裁决且全副本一致
✔ 数组中间插入：并发插入不丢失且定序确定
✔ fork 后互不影响：副本状态相互独立
ℹ pass 8 / fail 0
```
