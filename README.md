# 树文法与 XML/JSON 结构模式校验器

基于**树自动机（Tree Automata）**的层次数据结构校验器，使用 TypeScript 实现，
零第三方依赖，测试使用 Node.js 内置 `node:test`。

## 核心概念

- **树文法（Tree Grammar）**：一组产生式 `标签 -> 子节点序列正规模式`，
  整体构成一个树自动机——每个标签是自动机的状态，每条产生式的模式
  编译为 NFA，作为该状态的水平转移函数（hedge automaton 的标准构造）。
- **正规模式**：`lit / seq / alt / opt / star / plus / empty / text` 组合子，
  可表达“必须按序出现 title 后接 1 个或多个 paragraph”等内容模型。
- **精细诊断**：NFA 模拟失败时，根据自动机的期望集与后续集区分
  “必要元素缺失 / 顺序错误 / 多余元素”，并定位到出错路径。

## 目录结构

```
src/pattern.ts      正规模式组合子 + Thompson 构造编译 NFA + 带失败分析的模拟器
src/tree.ts         JSON -> 标签树映射（数组展开为重复兄弟元素）
src/grammar.ts      树文法定义（产生式编译为 NFA）
src/validator.ts    校验器：递归校验并生成精细诊断
src/book-schema.ts  书籍文档模式（验收边界）
examples/demo.ts    运行样例
test/validator.test.ts  Node.js 内置测试（node:test + assert）
```

## 书籍文档模式

```
book     -> title, author, chapter+     标题、作者，后接 1 个或多个章节
author   -> name, email                 作者必须按序包含姓名与邮箱
chapter  -> heading, paragraph+         章节标题后接 1 个或多个段落
title/name/email/heading/paragraph -> #text
```

## 环境要求

Node.js >= 22.6（利用 `--experimental-strip-types` 直接运行 TypeScript；
Node >= 23.6 可省略该标志）。开发验证环境：Node v23.9.0。

## 运行

```bash
npm test     # 运行内置测试
npm run demo # 运行样例
```

## 运行样例（npm run demo 实际输出）

```
== 合法书籍文档 ==
✓ 校验通过

== 缺失 author 的书籍文档 ==
✗ 校验失败，发现 1 处问题:
  路径 /book/author: 预期出现[name, email]，实际未找到
```

输入的缺失字段 JSON 树：

```json
{
  "book": {
    "title": "树自动机导论",
    "chapter": [{ "heading": "引言", "paragraph": ["第一段"] }]
  }
}
```

校验器定位到 `/book/author` 缺失，并输出精细诊断
`预期出现[name, email]，实际未找到`（author 的内容模型为 `name, email`）。

## 测试结果（npm test 实际输出）

```
✔ 合法书籍文档校验通过
✔ 缺失 author：定位到 /book/author 并给出精细诊断
✔ 缺失 author.email：定位到 /book/author/email
✔ 子元素顺序错误（email 先于 name）
✔ 缺少必要章节（chapter 至少 1 个）
✔ 出现未定义的多余元素
✔ chapter 缺失 heading
✔ 文本节点不允许包含子元素
✔ 根元素标签错误
✔ JSON -> 树映射：数组展开为重复兄弟元素
ℹ tests 10
ℹ pass 10
ℹ fail 0
```

## 诊断示例

| 输入问题 | 路径 | 诊断信息 |
| --- | --- | --- |
| 缺失 author | `/book/author` | `预期出现[name, email]，实际未找到` |
| 缺失 author.email | `/book/author/email` | `预期出现[email]，实际未找到` |
| email 先于 name | `/book/author` | `预期出现[name]，实际为'email'` |
| 缺少 chapter | `/book/chapter` | `预期出现[heading, paragraph+]，实际未找到` |
| 多余元素 isbn | `/book` | `预期出现[chapter]，实际为'isbn'` |
