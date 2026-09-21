# minilang

一个用 Python 3.11（仅标准库）手写的小型解释语言，实现了结构化的控制转移：
`throw` / `try` / `catch` / `finally` / `return` / `break` / `continue`。
词法、语法、解释执行全部自行实现，不依赖 `eval` 或任何现成编译器。

## 运行

```sh
python3.11 -m minilang examples/demo.ml
python3.11 -m unittest discover -s tests   # 运行测试
```

## 语言概览

```
let x = 1;                  // 变量声明（块级作用域）
x = x + 1;                  // 赋值
fn add(a, b) { return a + b; }
if (cond) { ... } else { ... }
while (cond) { ... break; ... continue; }
try { ... } catch (e) { ... } finally { ... }
throw "error";
print(x);                   // 内建函数 print / len
```

- 值类型：整数、浮点、字符串、布尔、`null`、函数（一等公民，支持闭包）。
- 运算符：`+ - * / %`、`== != < <= > >=`、`&& || !`、一元 `-`。
- 注释：`//` 与 `/* ... */`。
- 条件按真值判断：`false`、`null`、`0`、`""` 为假，其余为真。

## 源码位置

所有 token、AST 节点与错误都携带 UTF-8 **字节**区间 `[start, end)`。
含多字节字符（如中文）时区间仍然准确，CLI 报错同时给出 行:列 与字节区间：

```
examples/uncaught.ml:1:33: error: uncaught throw: 清理失败 [bytes 40..61]
  caused by: 原始错误
```

## 控制转移语义

控制转移按优先级解析（`minilang/interpreter.py` 中的 `resolve_transfer`）：

```
throw (4) > return (3) > break (2) > continue (1) > normal (0)
```

规则：

- **finally 必执行**：无论 try/catch 以何种方式离开（正常、break、continue、
  return、throw），finally 都会运行。
- **优先级覆盖**：finally 产生新转移时，与未决的旧转移比较，优先级高者生效；
  同优先级时新转移（finally 的）覆盖旧转移。
- **return 快照**：`return expr` 的表达式先求值再进入 finally，finally 修改
  外部状态不会改变已求值的返回结果（见 `examples/finally_priority.ml` 第 5 例）。
- **throw 覆盖保留 cause**：finally 抛错覆盖原错误时，原错误被追加到新错误的
  `cause` 链尾，顶层未捕获时完整打印。
- **嵌套清理顺序**：内层 finally 先于外层 finally 执行。

## 运行样例

`python3.11 -m minilang examples/demo.ml`：

```
sum of odd 1..10 = 25
fib(10) = 55
caught: empty flag
cleanup: parse_flag done
default
cleanup: parse_flag done
on
```

`python3.11 -m minilang examples/finally_priority.ml`：

```
return beats break
from finally
f3 escaped with: boom
outer catch: cleanup failed
f5() = 1 but x = 99
```

## 测试结果

```
$ python3.11 -m unittest discover -s tests
.................................................
----------------------------------------------------------------------
Ran 49 tests in 0.008s

OK
```

覆盖：词法字节区间（含多字节字符）、运算符优先级、闭包/递归、
break/continue、return 快照、finally 在各种离开方式下必执行、
优先级覆盖矩阵、throw 覆盖保留 cause 链、嵌套 finally 清理顺序、
运行时错误的字节区间定位。

## 目录结构

```
minilang/
  lexer.py         词法分析（UTF-8 字节区间）
  ast_nodes.py     AST 定义
  parser.py        递归下降语法分析
  interpreter.py   树遍历解释器 + 控制转移优先级解析
  __main__.py      CLI（行:列 + 字节区间报错，cause 链打印）
examples/          示例脚本
tests/             unittest 测试（49 个）
```
