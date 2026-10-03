# 树自动机结构模式校验器（tree-automata-validator）

基于**正规树自动机（regular tree automata）**的 XML/JSON 层次数据结构校验器。
为节点标签定义子节点序列的正规模式（如 `title` 后接 1 个或多个 `paragraph`），
校验复杂树结构并输出违反模式的路径与精细诊断。

纯 TypeScript、零依赖，利用 Node.js ≥ 22.6 的原生类型擦除直接运行 `.ts`。

## 核心概念

- **树自动机**：每条模式规则是一个状态（以节点标签命名）；规则中子节点序列
  正规式即"水平自动机"，决定该状态下合法的子节点标签序列。
- **子节点序列正规式**（`src/regex.ts`）：`seq` / `choice` / `star` / `plus` /
  `opt` / `label`，基于 **Brzozowski 导数**匹配——每消费一个标签即求导，
  剩余式可空则接受；剩余式同时用于推导"还缺哪些标签"的诊断。
- **统一树模型**（`src/tree.ts`）：JSON（对象/数组/原始值）与 XML（极简解析器）
  都归约为带标签有序树后走同一套校验。
- **诊断**（`src/validator.ts`）：自顶向下遍历，报告违规路径
  （如 `/book/author`，同名兄弟带序号 `/book/paragraph[2]`）与中文诊断消息。

## 书籍文档模式（`src/schemas/book-schema.ts`）

```
book      → title, author, paragraph+   // 标题、作者，后接 1 个或多个段落
author    → name, email                 // 姓名与邮箱，按序且必填
title/name/email/paragraph → 纯文本叶子
```

## 运行

```bash
npm test        # Node 内置测试运行器（node:test）
npm run sample  # 运行样例
```

## 样例输出

缺失必要字段的 JSON 树 `{ book: { title, author: {}, paragraph: [...] } }`：

```
=== JSON：缺失必要字段 ===
校验失败，共 1 处违规：
  路径 /book/author
    预期出现[name, email]，实际未找到
```

诊断类型：

| 场景 | 消息示例 |
| --- | --- |
| 缺失必要子节点 | `预期出现[name, email]，实际未找到` |
| 子节点顺序/标签错误 | `预期出现[title]，实际出现 'author'` |
| 多余子节点 | `此处不允许再出现子节点，实际出现 'extra'` |
| 未定义标签 | `未定义的标签 'extra'，模式中没有对应规则` |
| 文本约束 | `预期包含文本内容，实际未找到` |
| 根标签不匹配 | `根标签不匹配：预期 'book'，实际为 'magazine'` |

## 目录结构

```
src/regex.ts               子节点序列正规式（导数匹配 + 诊断推导）
src/tree.ts                带标签有序树；JSON → 树；XML → 树
src/schema.ts              模式（树自动机）定义与规则构造器
src/validator.ts           自顶向下校验器，产出路径 + 诊断
src/schemas/book-schema.ts 书籍文档模式
examples/run-sample.ts     运行样例
test/*.test.ts             node:test 测试（24 个用例）
```
