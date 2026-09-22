# MiniLang

一个手写的小型语言与树遍历解释器（Python 3.11，仅标准库），支持
`throw` / `try` / `catch` / `finally`、`return`、`break` / `continue`
循环控制。词法器、递归下降解析器、解释器全部自行实现——不使用
`eval`、`compile` 或任何现成编译器/解析器生成器。

## 运行

```sh
python3.11 main.py examples/demo.ml              # 语言特性总览
python3.11 main.py examples/finally_control.ml   # finally 覆盖优先级
python3.11 main.py examples/nested_finally.ml    # 嵌套清理顺序
python3.11 main.py examples/uncaught_cause.ml    # 未捕获错误与 cause 链
```

## 测试

```sh
python3.11 -m unittest discover -s tests -v
```

## 语言速览

```js
let x = 1;
fn add(a, b) { return a + b; }
if (x > 0) { print("pos"); } else { print("non-pos"); }
while (x < 10) { x = x + 1; }
for (let i = 0; i < 10; i = i + 1) {
    if (i == 3) { continue; }
    if (i == 8) { break; }
}
try {
    throw "boom";
} catch (e) {
    print("caught:", e);
} finally {
    print("always");
}
```

值：整数、浮点、字符串、布尔、`nil`、函数（一等公民，支持闭包）。
只有 `nil` 和 `false` 为假。内置函数 `print(...)`。注释：`//` 与 `/* */`。

## 控制转移语义（核心规则）

所有语言级控制转移（`break` / `continue` / `return` / `throw`）统一表示为
`ControlTransfer` 对象，`try` 的执行遵循明确的优先级规则：

1. **`finally` 必执行**：无论 try/catch 以何种方式完成（正常、throw、
   return、break、continue），`finally` 块都会运行。
2. **`finally` 的新转移覆盖旧转移**：若 `finally` 块自身以新的控制转移
   T_new 结束（throw / return / break / continue 均可），T_new 丢弃并
   替换任何待传播的转移 T_old。若 `finally` 正常结束，T_old 继续传播。
3. **cause 保留**：当 T_new 是 throw 且 T_old 也是 throw 时，原错误作为
   `T_new.cause` 保留，可形成嵌套覆盖的完整错误链。
4. **return 先求值**：`return expr` 在任何 `finally` 运行之前完成求值，
   因此 `finally` 修改外部状态（如给变量赋新值）不影响已算出的返回值。
5. **嵌套清理由内向外**：内层 `finally` 先于外层执行，每层都有独立的
   覆盖机会。
6. `catch` 只捕获 throw（含运行时错误，如除零、未定义变量），不拦截
   break / continue / return。

## 源码位置：UTF-8 字节区间

每个 token 与 AST 节点都携带 `(start, end)`，表示源码的 UTF-8 **字节**
区间（`start` 含、`end` 不含），而非字符下标。词法器预计算
"字符下标 → 字节偏移" 表，多字节字符（如中文标识符、字符串中的 é）
也能得到正确的字节区间。所有语法错误、运行时错误、未捕获 throw 都以
字节区间报告位置（CLI 同时换算出行列号方便阅读）。

## 项目结构

```
minilang/
  lexer.py      # 手写词法器，token 携带 UTF-8 字节区间
  ast_nodes.py  # AST 节点定义（均含字节区间）
  parser.py     # 手写递归下降解析器
  interp.py     # 树遍历解释器（控制转移语义在此）
  errors.py     # ControlTransfer 体系与 LangThrow.cause 链
main.py         # CLI：运行源文件并报告错误（含 cause 链）
examples/       # 可运行示例
tests/          # unittest 测试套件
```
