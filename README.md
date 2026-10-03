# Verifiable Merkle Patricia Trie (TypeScript)

带范围摘要的可验证前缀树：结合字典树路径压缩与默克尔树哈希承诺的紧凑数据结构。
纯 Node.js 内置模块实现（`node:crypto` / `node:test`），零依赖，Node ≥ 22.6 直接运行 TypeScript。

## 特性

- **路径压缩**：共享前缀收缩为 Extension Node，键尾收缩为 Leaf Node，分叉处为 16 路 Branch Node
- **默克尔承诺**：`hash = sha256(canonical(node) + rangeSummary)`，任何节点变更级联更新根哈希
- **范围摘要**：每个节点承诺子树的 `[minKey, maxKey]` 与键数量，摘要参与哈希计算，不可伪造
- **可验证证明**：`getProof(key)` 输出根到叶的完整节点序列（含兄弟节点摘要），
  `verifyProof` 重算哈希链，篡改父级哈希/节点内容/摘要均会被识破
- **前缀检索**：`keysWithPrefix(prefix)` 按字典序返回匹配键值对

## 运行

```sh
npm test    # node --test "test/*.test.ts"
npm run demo
```

## 结构

- `src/encoding.ts` — 键 ↔ nibble 路径编码
- `src/nodes.ts` — 节点类型、规范化序列化、哈希与范围摘要
- `src/trie.ts` — 插入（自动分裂/压缩）、查询、前缀检索、证明生成
- `src/proof.ts` — 证明验证（哈希链 + 摘要重算 + 键值匹配）
- `src/demo.ts` — 运行样例
- `test/mpt.test.ts` — Node.js 内置测试（11 个用例）
