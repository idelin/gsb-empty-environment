# RGA 协作文本列表（TypeScript）

RGA（Replicated Growable Array）—— 基于因果树的协同富文本底层数组，满足 CRDT 强最终一致性。

## 设计

- **链表节点组织字符**：每个字符一个节点，含唯一标识 `id = [counter, replicaId]`（Lamport 式）。
- **因果前驱**：每个节点记录 `left`（插入点左侧节点的 id），全体节点构成因果树；链表即其前序遍历。
- **并发排序**：同一前驱下的并发插入按标识**降序**排列，保证所有副本收敛到同一序列。
- **墓碑删除**：删除仅打标记，因果引用永久有效。

## 运行

环境要求：Node.js ≥ 22.6（利用内置 TypeScript 类型擦除直接运行 `.ts`，无需编译；推荐 ≥ 23.6 免 flag）。

```bash
npm run demo   # 运行协同编辑样例
npm test       # Node.js 内置测试（node:test）
```

可选类型检查（需自行安装 TypeScript）：`npx tsc --noEmit`

## 验收场景

两人协同编辑文档 `AB`：用户1 在 A 后插入 `X`，用户2 在 A 后插入 `Y`。
合并后两端稳定展示同一结果（`AXYB` 或 `AYXB`，由标识序决定，本实现中
`user2 > user1` 故为 `AYXB`），绝无字符交错或乱序。见 `src/demo.ts` 与
`test/rga.test.ts` 中的验收用例。

## 文件

- `src/rga.ts` —— RGA 核心实现
- `src/demo.ts` —— 运行样例
- `test/rga.test.ts` —— 单元测试 + 100 轮随机并发收敛模糊测试
