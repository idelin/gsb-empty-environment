# exprfmt — 双向解析器与 AST 美化打印器

纯 Go 标准库实现的小型表达式语言内核：

- **解析器**（`lexer.go` / `parser.go`）：词法分析 + 优先级爬升（Pratt）解析，
  支持数字、字符串、标识符、一元 `-`/`!`、二元运算符（`|| && == != < <= > >= + - * / %`）、
  函数调用与括号；括号不进入 AST，由打印机按需重新推导。
- **美化打印器**（`printer.go`）：维护缩进层级（每层 2 空格）与 80 列行宽限制；
  一行放不下时，自动在合法的运算符或逗号处折行——二元表达式在运算符后换行、
  右操作数缩进一级，函数调用每参数一行、尾逗号收尾；需要括号保持结构时括号独占成行。
- **往返同构**：`Parse(src) → Format → Parse` 得到的 AST 与原 AST 完全一致
  （`reflect.DeepEqual`），由表格用例 + 2000 个随机深层 AST 的属性测试保证。

## 运行样例

```sh
go run ./cmd/demo
```

输出：

```
=== formatted (80-column limit) ===
processOrder(
  validateCustomer(customerName, customerEmail, customerAddress),
  calculateSubtotal(
    itemPrice * itemQuantity,
    discountPercent * itemPrice,
    loyaltyPoints / redemptionRate,
  ),
  applyShipping(
    destinationCountry,
    packageWeight,
    packageDimensions,
    expressDelivery,
  ),
  finalizePayment(cardNumber, expiryDate, securityCode, billingAddress),
) &&
  status == "active" ||
  overrideApproved

longest line: 72 columns
round-trip AST identical: true
```

## 测试结果

```sh
go test ./...
```

```
ok  	exprfmt	0.382s
?   	exprfmt/cmd/demo	[no test files]
```

覆盖的测试（`exprfmt_test.go`）：

- `TestFormatFlat` / `TestFormatMinimalParens`：短表达式保持单行、最少括号重推导。
- `TestFormatBreaksBinary` / `TestFormatBreaksCall`：超长行在运算符 / 逗号处折行的黄金输出。
- `TestRoundTripTable`：手工构造的嵌套用例往返一致。
- `TestLineWidthDeepNesting` / `TestLineWidthLongChain`：深度嵌套与长链表达式，
  断言所有行 ≤ 80 列且往返一致。
- `TestRandomRoundTrip`：2000 个随机生成（深度 5）的 AST，打印后再解析必须完全相等。
- `TestParseErrors`：非法输入返回错误。
