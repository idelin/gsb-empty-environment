# RGA 协作文本列表（TypeScript）

基于因果树的协同富文本底层数组（Replicated Growable Array）实现。

## 设计

- 每个字符是一个链表节点，携带全局唯一标识 `id = (counter, site)`：
  - `counter` 为 Lamport 时钟（父节点 counter + 1），保证因果后继的 id 恒大于其因果前驱；
  - `site` 为站点标识，并发同值时确定性决胜。
- 节点记录因果前驱（`parentId`），构成因果树；链表按 RGA 规则展开。
- 并发在同一点插入时，兄弟节点按 id **降序**排列（id 大者靠前），所有副本收敛到同一顺序。
- 删除采用墓碑（tombstone）；删除先于插入到达时记录 id，节点就绪后补标。
- 操作幂等；因果乱序（子先于父到达）通过挂起队列自动补挂。

## 目录

- `src/rga.ts` — RGA 核心实现
- `test/rga.test.ts` — Node.js 内置测试（`node:test` + `assert`）
- `examples/demo.ts` — 验收场景运行样例

## 运行

无需安装依赖（Node ≥ 22.18 / 23.6 原生支持 TypeScript 类型擦除）：

```sh
npm test        # 运行测试
npm run demo    # 运行验收样例
```

## 运行样例输出

```
初始文档:  user1="AB"  user2="AB"
并发编辑:  user1="AXB" (插入 X, id=3@u1)
            user2="AYB" (插入 Y, id=3@u2)
合并结果:  user1="AYXB"  user2="AYXB"
✔ 两端一致，无交错、无乱序
```

## 测试结果

```
✔ 验收场景：两人在 A 后并发插入 X / Y，合并后两端一致且无交错
✔ 操作投递顺序颠倒仍收敛
✔ 并发插入块不交错：各自插入多字符片段
✔ 重复投递幂等
✔ 因果乱序投递：子操作先于父操作到达
✔ 删除：墓碑机制与并发插入
✔ 随机并发操作收敛性（多副本、乱序投递）
✔ compareIds 降序语义
ℹ tests 8
ℹ pass 8
ℹ fail 0
```
