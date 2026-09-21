# minilang:增量解析小语言

一个带括号、函数调用和声明的小语言,纯 Python 3.11 标准库实现,
不使用 `eval` 或任何现成编译器。

## 语言示例

```
# 注释(支持中文,位置按 UTF-8 字节计)
def area(r) = 3.14 * r * r;
def double(x) = x + x;
let a = area(2);
let b = double(a);
```

文法:`def` 函数声明、`let` 变量声明、`+ - * /`、一元负号、
括号分组、`f(x, y)` 调用、`#` 行注释。

## 特性

- **增量解析**:`Document.apply_edit(start, end, replacement)` 按
  UTF-8 字节区间应用编辑;与编辑范围不相交的顶层声明子树原样复用
  (同一 Python 对象,可用 `is` 判定),只有相交的声明被重新解析。
  在函数体内改一个数字,无关函数节点不会被重建。
- **位置模型**:节点只存自身长度和相对父节点的偏移,声明间隙存在
  `Program.gaps`;绝对 UTF-8 字节区间由 `compute_positions` 按需计算,
  编辑后自动更新,复用节点无需任何修改。
- **错误恢复**:未闭合括号、缺少 `;`、参数列表未闭合、顶层意外内容
  都会产生明确的 `ErrorNode`(带字节区间),并同步到 `;` / `def` /
  `let` 后继续解析后续声明。
- **一致性**:`tree_signature` 可比较增量树与全量树的语义结构,
  测试中每次编辑后都校验两者一致。
- **解释执行**:`evaluate(program)` 直接在语法树上求值,运行期错误
  (未定义变量、除零、参数个数不符、无限递归)带字节区间报错;
  含错误节点的程序会被拒绝执行。

## 运行

```
python3 demo.py                 # 运行样例
python3 -m unittest discover -v # 运行测试(26 个用例)
```

## 代码结构

- `minilang/lexer.py` — 词法分析,token 位置为 UTF-8 字节区间
- `minilang/nodes.py` — 不可变语法树节点、位置计算、结构签名
- `minilang/parser.py` — 递归下降解析(全量/增量)、错误恢复
- `minilang/document.py` — 编辑应用与子树复用
- `minilang/interpreter.py` — 树遍历解释器
- `tests/` — unittest 测试(解析、增量、解释器)
