# Bidirectional Parser & AST Pretty-Printer

一个“双向”内核：递归下降解析器把表达式源码解析为 AST，美化打印器再把
AST 还原为格式化源码。打印遵循 80 列行宽限制，超限时自动在合法的运算符
或逗号处折行并保持缩进；输出可被再次解析，得到与原 AST **结构完全同构**
的语法树。

## 语言

```
expr    := orExpr
orExpr  := andExpr ("||" andExpr)*
andExpr := eqExpr ("&&" eqExpr)*
eqExpr  := relExpr (("==" | "!=") relExpr)*
relExpr := addExpr (("<"|"<="|">"|">=") addExpr)*
addExpr := mulExpr (("+"|"-") mulExpr)*
mulExpr := unary  (("*"|"/"|"%") unary)*
unary   := ("-"|"!") unary | postfix
postfix := atom ("(" args? ")")*
atom    := number | string | "true" | "false" | ident | "(" expr ")"
```

AST 节点：`num` / `str` / `bool` / `ident` / `unary` / `binary` / `call`
（见 `src/ast.ts`）。

## 设计

- **解析器**（`src/parser.ts`）：优先级上升的递归下降；二元运算全部左结合。
- **美化打印器**（`src/printer.ts`）：Wadler 风格的 Doc 代数
  （`text` / `concat` / `line` / `softline` / `group` / `indent`）。
  `group` 先尝试平铺，超出 80 列时在该组的 `line` / `softline` 处换行；
  换行点只出现在二元运算符与调用参数逗号之后，因此折行永远落在合法
  语法边界上。运算符引领续行、参数逐行缩进两个空格。`fits` 判定会把
  当前行剩余的全部命令（如收尾的 `)`）一并计入宽度。
- **括号策略**：打印器按优先级与结合性保守补括号（右侧同级一律加括号），
  保证 `parse(prettyPrint(ast))` 与 `ast` 深度相等。
- 数字字面量须为非负有限值（负数用一元 `-` 表示）；字符串按 JSON 转义。

## 运行

需要 Node.js ≥ 22.18（内置 TypeScript 类型擦除，无需编译步骤）。

```sh
npm test        # node --test（Node.js 内置测试运行器）
npm run demo    # 运行样例：深度嵌套 AST -> 美化打印 -> 二次解析校验
```

## 运行样例

`npm run demo` 输出（深度嵌套表达式，80 列限制）：

```
sigmoid(
  weight_feature_cross
    * tanh((user_age_years - 12.5) / sqrt(variance_age + 0.0001))
    + lookup_embedding("user_segment", hash_bucket(user_id, 64))
      * gate(
        is_active && !is_blocked,
        has_coupon || cart_total > 99,
        experiment_group % 2 == 0,
        true
      )
)
lines: 11, longest line: 65 columns
round-trip OK: re-parsed AST is structurally identical
```

## 测试结果

`npm test`（node:test，共 18 个用例，全部通过）：

- 解析器：字面量 / 转义字符串 / 优先级 / 左结合 / 括号 / 一元运算 / 调用 / 错误输入
- 打印器：短表达式单行、长运算链在运算符处折行并缩进、长参数列表在逗号处
  折行、嵌套组由外向内逐级换行、必要的括号保留、所有输出行 ≤ 80 列
- 往返同构：手工构造的深度嵌套 AST、右嵌套运算符、以及 500 个随机生成
  AST（深度 2–7）与 100 个 40 列窄宽度用例，`parse(prettyPrint(ast))`
  均与 `ast` 深度相等

```
ℹ tests 18
ℹ pass 18
ℹ fail 0
```

## 目录

```
src/ast.ts      AST 类型与构造器
src/lexer.ts    词法分析
src/parser.ts   语法解析（优先级上升）
src/printer.ts  Doc 代数 + 80 列美化打印
src/index.ts    公共导出
test/           node:test 测试（解析 / 打印 / 往返同构 + 随机属性测试）
examples/demo.ts 运行样例
```
