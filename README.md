# modlang

一个用 Python 3.11（仅标准库）实现的迷你语言与模块系统。词法分析、
语法分析、解释执行全部手写，不使用 `eval`/`exec` 或任何现成编译器。
所有源码位置均以 UTF-8 字节区间（`start..end`）报告。

## 语言概览

```
import { isEven } from "b";        # 显式导入（语句以 ; 结尾）

export const answer = 42;          # 显式导出常量
var acc = 0;                       # 模块局部可变变量

export fn isOdd(n) {               # 函数声明会被提升（hoist）
    if n == 0 { return false; }
    return isEven(n - 1);
}

while acc < 10 { acc = acc + 1; }
print(acc, "done");
```

- 值：整数、浮点、字符串、布尔、`nil`、函数（闭包）。
- 运算：`+ - * / %`、`== != < <= > >=`、`and or not`、一元 `-`。
- 控制流：`if/else`、`while`、`return`；`#` 为行注释。

## 模块语义

- **显式导入/导出**：`import { ... } from "mod"` 只能拿到对方
  `export` 的名字；其余绑定是模块私有的。
- **初始化图**：加载时按 import 边构建依赖图（`Module.deps`），
  初始化按图深度优先进行。
- **函数互引**：函数声明在模块初始化开始时即被提升，因此
  `a` 导入 `b` 的函数、`b` 导入 `a` 的函数可以正常链接，
  跨模块互递归也能运行。
- **拒绝未初始化读取**：顶层 `const`/`var` 先以“未初始化”占位
  （TDZ）。循环中读到尚未初始化的常量会报错并给出读取链，例如：
  `cannot read 'a.x': module 'a' is still initializing; read chain: b.y -> a.x`。
- **只初始化一次**：模块按绝对路径缓存；重复导入（包括环上的
  回边）不会重复执行模块顶层副作用。

## 运行

```
python3.11 -m modlang examples/main.mod   # 运行入口模块
python3.11 run_sample.py                  # 两个演示样例
python3.11 -m unittest discover -s tests -v
```

## 代码结构

- `modlang/lexer.py` — 词法分析，token 携带 UTF-8 字节区间
- `modlang/parser.py` — 递归下降语法分析，生成 AST
- `modlang/ast_nodes.py` — AST 节点定义
- `modlang/interpreter.py` — 树遍历解释器：环境、TDZ、惰性导入
  绑定（`ImportRef`）、读取链报告
- `modlang/loader.py` — 模块加载、初始化图、只初始化一次的缓存
- `examples/` — 跨模块函数环（`a`⇄`b`）与循环常量报错样例
- `tests/test_modlang.py` — unittest 测试（14 个用例）
