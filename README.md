# pretty — 双向解析器与 AST 美化打印器

从 AST 还原格式化代码的逆向工程内核：解析器把表达式文本构造成 AST，
美化打印器再把 AST 还原为带缩进、受行宽约束的代码；打印输出二次解析后
与原 AST 结构同构（`reflect.DeepEqual` 成立）。

## 语言

标识符、数字、字符串、一元运算（`-` `!`）、二元运算
（`|| && == != < <= > >= + - * / %`，按优先级结合）、函数调用与括号。

## 打印策略

- 行宽限制默认 80 列（`pretty.DefaultWidth`，可用 `PrintWidth` 自定义）。
- 子表达式平铺后能放入当前行则平铺；否则在合法的**二元运算符之后**
  或**逗号之后**折行，续行缩进一个层级（4 空格）。
- 按优先级自动补括号，保证重新解析后 AST 结构完全一致。

## 运行样例

```sh
go run ./cmd/demo
```

输出（深度嵌套表达式按 80 列折行，逗号处断行并缩进续行）：

```
=== pretty-printed (80-column limit) ===
compute(alpha + beta * gamma - delta / (epsilon + zeta),
    transform(x1, x2, x3) * (foo(bar(1, 2), baz(3)) + quux) % 17,
    !(flag1 && flag2) || (score >= threshold && level <= max_level) == done,
    -"negated" + wrap(nested(deep(deeper(deepest(a, b), c), d), e), f) * 3.14)

=== round-trip check ===
OK: re-parsed AST is structurally identical to the original
```

## 测试

```sh
go test ./...
```

结果：

```
--- PASS: TestParsePrecedence
--- PASS: TestParseCallAndUnary
--- PASS: TestParseErrors
--- PASS: TestRoundTrip
--- PASS: TestWidthLimit
--- PASS: TestBreaksAtOperatorsAndCommas
--- PASS: TestFuzzRoundTrip
ok  	example.com/pretty/pretty
```

测试覆盖：优先级与结合性、语法错误、往返一致性（parse → print → parse
AST 相同）、80 列行宽与续行缩进、打印幂等性，以及 500 个随机生成的
深层 AST 的模糊往返测试。
