# Verifiable Merkle Patricia Trie (TypeScript)

带范围摘要的可验证前缀树：结合字典树与默克尔树的紧凑数据结构，零依赖，
仅使用 Node.js 内置模块（`node:crypto`、`node:test`）。

## 特性

- **路径压缩**：共享前缀收缩为 Extension Node，末端剩余路径存于 Leaf Node，
  分叉处使用 16 槽 Branch Node（nibble 级别）。
- **默克尔化**：每个节点携带 SHA-256 哈希，任何键值变更沿路径级联更新根哈希；
  相同数据集任意插入顺序得到相同根哈希。
- **范围摘要**：每个节点聚合子树的 `{ count, minKey, maxKey }`，并纳入哈希，
  摘要篡改同样会破坏根哈希。
- **前缀检索**：`getByPrefix(prefix)` 沿前缀下钻后收集子树，按字典序返回。
- **可验证证明**：`prove(key)` 输出根到叶的完整节点序列；
  `verifyProof(rootHash, proof)` 重放哈希链，父级哈希、范围摘要、
  叶子值任一被篡改即验证失败。

## 环境要求

Node.js >= 23.6（原生 TypeScript 类型擦除，无需编译与任何依赖安装）。

## 运行

```bash
npm test    # node --test test/*.test.ts
npm run demo
```

## API

```ts
import { MerklePatriciaTrie } from './src/trie.ts';
import { verifyProof } from './src/proof.ts';

const trie = new MerklePatriciaTrie();
trie.put('account.balance', '1000');
trie.get('account.balance');        // Buffer
trie.getByPrefix('account.');       // Entry[] 按 key 排序
trie.rootHash;                      // 默克尔根
trie.summary;                       // { count, minKey, maxKey }
const proof = trie.prove('account.balance'); // 根到叶节点序列
verifyProof(trie.rootHash!, proof); // true
```

## 结构

- `src/nibbles.ts` — 键到 nibble 路径的编码工具
- `src/node.ts` — Leaf / Extension / Branch 节点、哈希与范围摘要聚合
- `src/trie.ts` — 插入（路径压缩/分裂）、查询、前缀检索、证明生成
- `src/proof.ts` — 证明序列化与 `verifyProof` 验证
- `test/mpt.test.ts` — 12 个 Node.js 内置测试
- `examples/demo.ts` — 完整运行样例（含篡改检测演示）
