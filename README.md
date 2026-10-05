# quorum-check — 法定集合相交性检查工具

判断读/写法定集合（quorum）族是否满足相交性：

- **rw**：任意读集合 R 与任意写集合 W，是否必有 R ∩ W ≠ ∅
- **ww**：任意两个写集合 W₁、W₂，是否必有 W₁ ∩ W₂ ≠ ∅

节点数 ≤ 20。法定集合族用**单调阈值公式**压缩描述（如“至少选某组若干个”，可嵌套），
工具**不会**枚举全部节点子集再筛选，而是分支限界 + 记忆化搜索。

- 若性质成立：返回**证明统计**（剪枝搜索树的规模，即不可行性证书）。
- 若性质不成立：返回**字典序最小**的不相交见证对 `(A, B)`。

## 运行

无需编译（Node ≥ 23 直接执行 TypeScript）：

```bash
node src/cli.ts <config.json|-> [--check rw|ww|both]
npm test          # 运行测试（含与暴力枚举的随机对拍）
```

退出码：全部检查相交 → 0；发现不相交见证 → 1；用法/输入错误 → 2。

## 输入格式

```json
{
  "nodes": 5,
  "read":  { "atLeast": 3, "group": [0, 1, 2, 3, 4] },
  "write": { "all": [ { "k": 2, "of": [0, 1, 2] }, { "any": [3, 4] } ] }
}
```

公式（可任意嵌套）：

| 形式 | 含义 |
| --- | --- |
| `7` | 单节点：集合包含节点 7 |
| `{ "atLeast": k, "group": [...] }` | 组内至少选 k 个 |
| `{ "k": k, "of": [子公式...] }` | 子公式至少 k 个成立 |
| `{ "any": [...] }` | 至少 1 个成立 |
| `{ "all": [...] }` | 全部成立 |

一个集合是某族的法定集合，当且仅当它满足对应公式（单调：超集仍是法定集合）。

## 输出

```json
{
  "nodes": 5,
  "checks": {
    "rw": { "intersects": true,
            "proof": { "nodesVisited": 22, "pruned": 21, "memoHits": 8,
                       "memoStates": 14, "maxDepth": 4, "elapsedMs": 0.9,
                       "method": "branch-and-bound+memoization" } },
    "ww": { "intersects": false,
            "witness": { "a": [0, 1], "b": [2, 3] },
            "search": { "oracleCalls": 10, "nodesVisited": 8, "...": "..." } }
  }
}
```

## 算法

**存在性搜索**（`src/search.ts`）：按节点顺序把每个节点指派为
“在 A / 在 B / 两侧都不在”（天然保证 A ∩ B = ∅），沿搜索树维护两侧公式的
满足状态（已满足 / 已违反 / 未定），任一违反即剪枝；一侧已满足后该侧分支不再展开。
失败状态按“每个阈值节点的（已满足子项数, 已违反子项数）向量”做记忆化，
相同剩余子问题不重复搜索。性质成立时，这棵完全剪枝的搜索树即为证明，其统计量随结果返回。

**字典序最小见证**（`src/witness.ts`）：集合按升序列表做字典序比较
（如 `[0,1] < [0,1,2] < [0,2] < [1]`），先最小化 A 再最小化 B。
采用前缀贪心：逐位尝试“集合到此结束 / 下一个元素取 v”，
每步用上面的存在性搜索作可行性预言机，最多 O(n²) 次预言机调用。

## 示例

```bash
node src/cli.ts examples/majority5.json      # 3-of-5 多数派：rw/ww 均相交
node src/cli.ts examples/two-of-five.json    # 2-of-5：见证 [0,1] 与 [2,3]
node src/cli.ts examples/nested.json --check ww
```

## 文件结构

- `src/formula.ts` — 公式类型、JSON 解析校验、部分指派下的状态求值
- `src/search.ts` — 分支限界 + 记忆化的不相交对存在性搜索
- `src/witness.ts` — 字典序最小见证构造（贪心 + 可行性预言机）
- `src/check.ts` — 顶层检查入口（证明统计 或 见证）
- `src/cli.ts` — 命令行界面
- `test/run-tests.ts` — 固定用例 + 随机小实例与暴力枚举对拍
