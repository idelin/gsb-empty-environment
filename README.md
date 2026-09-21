# call-by-need 表达式图（Python 3.11，零第三方依赖）

一个自行实现语法与解释执行的小型惰性语言：绑定只在被使用时才求值，
每个 thunk 只求值一次（值和异常都会缓存），求值过程中的自引用会被
检测为“黑洞”，并输出带源码位置的强制求值环；调试轨迹可以看到
完整的强制求值链。

不使用 `eval`、`exec` 或任何编译器/解析器第三方库；只用标准库
（`dataclasses`、`typing`、`unittest` 等）。源码位置全部使用
**UTF-8 字节区间**（`[start, end)`，行列按字节偏移计算）。

## 运行

```bash
python3.11 demo.py                      # 运行样例
python3.11 -m unittest discover -s tests -v   # 运行测试
```

## 语言概览

```
program := ('let' name '=' expr)* expr
expr    := 'let' ['rec'] binding {'and' binding} 'in' expr   # 惰性 let（支持互递归）
         | 'if' expr 'then' expr 'else' expr
         | expr ('||' | '&&' | 比较 | '+' '-' | '*' '/' '%') expr
         | '-' expr | INT | 'true' | 'false' | 'tick()' | name | '(' expr ')'
binding := name '=' expr
```

- 整数（`//` 语义的 `/`、`%`）、布尔、算术/比较/短路逻辑、一元负号。
- `#` 行注释（注释中可含任意 UTF-8 字符，不影响字节偏移）。
- `tick()`：零参数内建，每次被**真正执行**时全局计数 +1，用于验证共享。
- 顶层 `let` 是非递归顺序绑定；表达式内 `let ... in` 同样默认非递归，
  加 `rec` 并可用 `and` 声明互递归绑定组。

## 核心语义

1. **call-by-need 图**：每个绑定对应一个 `Thunk` 节点
   （表达式 + 词法环境）。变量引用即沿 `Env` 父链找到同一个节点，
   所以一个绑定被引用多少次都共享同一个 thunk。
2. **只求值一次**：状态机 `suspended -> running -> done`。
   首次 `force()` 才执行表达式；之后命中缓存。
3. **异常也缓存**：除零等 `EvalError` 会让 thunk 进入 `done` 并保存
   异常对象；之后每次引用重抛**同一个异常对象**，不会重新计算。
4. **黑洞检测**：`running` 状态的 thunk 再次被 `force()` 即构成环。
   从轨迹器的强制栈切出从首次进入该 thunk 到当前的链，生成
   `a -> b -> ... -> a` 形式的明确诊断（每环带 UTF-8 字节位置）。
   黑洞异常本身也被缓存。
5. **未使用不报错**：`if` 未选中的分支、`&&`/`||` 短路掉的一侧、
   从未引用的绑定都不会被强制——因此 `let boom = 1 / 0` 不使用就不出错。

## 代码结构

- `lazygraph/sourcemap.py`：UTF-8 字节区间 `Span` 与行列/片段换算。
- `lazygraph/lexer.py`：字节流词法分析，token 带字节区间。
- `lazygraph/ast.py` / `lazygraph/parser.py`：AST 与递归下降解析器。
- `lazygraph/interpreter.py`：`Env`、`Thunk`、`Evaluator`、`Tracer`、
  `Interpreter`（call-by-need 核心、黑洞检测、调试轨迹）。
- `lazygraph/errors.py`：错误层级与 `BlackHoleError` 环诊断。
- `tests/test_lazygraph.py`：27 个 `unittest` 用例。
- `demo.py`：6 个运行样例；输出见 `demo_output.txt`，测试结果见
  `test_results.txt`。
