# expr-edit

配置表达式编辑工具：读入带行注释的表达式，修改指定 AST 节点，输出可再次解析的文本。

## 语言

标识符、十进制整数、函数调用、括号、一元负号、`+ - * /`。
二元运算左结合，乘除优先于加减。行注释以 `//` 开头（因此除法会排版成
`a / b`，避免与注释混淆）。不依赖任何 parser 库。

## 注释归属规则

- 同行尾注释附在该行最后一个 token 之后（归属该行末尾所在的表达式节点，
  例如 `f(a, // c` 中注释属于参数 `a`）；
- 独立行注释附在后续 token 之前（作为该节点的 leading comments）；
- 文件尾没有后续 token 的注释保留在文档尾部（`doc.tail`）。

## 格式化与编辑保证

- 格式化只改变空白：不丢注释、不改变 AST、不合并 token；
- `parse(print(ast))` 结构等价，且 `format` 幂等；
- `replaceNode` 替换节点时保留其外部注释（leading/trailing），
  未修改节点的注释及顺序不变；
- 解析错误报告最早无法继续的位置（offset/line/col）与期望 token 集合。

## API

```js
import { parse, print, format, evaluate, find, replaceNode, parseExpr } from './index.js';

const doc = parse('x + (a - b) // keep\n');
const target = find(doc.root, (n) => n.kind === 'paren');
doc.root = replaceNode(doc.root, target, parseExpr('c * d'));
process.stdout.write(print(doc)); // 注释 // keep 保留
```

## CLI

```sh
node bin/expr-edit.js format <file>
node bin/expr-edit.js replace <file> <match-expr> <replacement-expr>
```

## 测试

需要 Node >= 18（使用内置 `node:test`）：

```sh
npm test
```
