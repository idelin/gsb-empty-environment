# modlang：带模块系统的小型解释器

纯 Python 3.11 标准库实现（词法分析 → 递归下降解析 → 树遍历解释），不使用
`eval` 或任何现成编译器。所有源码位置一律以 UTF-8 字节区间报告。

## 语言语法

```
import { name, ... } from "module"     // 显式导入（模块级）
export const x = <expr>                // 显式导出常量
export fn f(a, b) { <stmt>* }          // 显式导出函数
const x = <expr>                       // 模块/函数局部常量
return <expr>?  |  if <expr> {..} else {..}  |  <expr>   // 语句
```

表达式：数字、字符串（支持 `\n \t \" \\` 转义）、`true/false`、标识符
（允许 Unicode）、调用，以及 `or and == != < <= > >= + - * / %` 与一元
`- not !`。`//` 为行注释。内建函数 `print(...)`。

## 模块语义

- 模块在首次被导入时惰性初始化并缓存，顶层副作用（如 `print`）只执行一次。
- 函数声明先于一切顶层代码提升（hoist），因此函数可互引，包括跨导入环互引。
- `const` 绑定先以“未初始化”状态登记；在初始化完成前读取即报错。
  跨模块循环读取未初始化常量时，错误附带初始化链（如 `a -> b -> a`）。
- `import { x } from "m"` 建立活别名：读取时穿透到导出模块的绑定，
  只要读取时目标已初始化即可链接成功。
- 导入未导出的名字、读取未定义名字、自引用初始化器均为错误，
  并携带所在模块与 UTF-8 字节区间。

## 运行

```
python3.11 main.py <目录> <入口模块>     # 目录下的 *.mm 构成模块集合
python3.11 -m unittest discover -s tests -v
```

## 运行样例（实测输出）

```
$ python3.11 main.py examples/mutual_fn a        # A 导入 B 函数且 B 导入 A 函数
true

$ python3.11 main.py examples/const_cycle a      # 循环读取未初始化常量
error: imported binding 'x' from module 'a' is not initialized yet in module 'b' at bytes 40..41 initialization chain: a -> b -> a

$ python3.11 main.py examples/side_effects main  # lib 被两个模块导入，只初始化一次
lib initialized
43
44

$ python3.11 main.py examples/utf8 main          # Unicode 标识符与字符串
你好，世界
```

## 测试结果

`python3.11 -m unittest discover -s tests -v`：16 个测试全部通过（OK）。
覆盖：多字节字符的 UTF-8 字节区间、运算符优先级与 AST 区间、函数互引
导入环链接、常量环初始化链报错、重复导入单次初始化、未初始化读取、
自引用初始化器、导入未导出名字、函数提升前向引用、字节区间错误定位、
模块局部绑定私有性、Unicode 标识符、缺失模块、递归与控制流。

## 代码结构

- `modlang/lexer.py` — 分词器，token 携带 UTF-8 字节区间
- `modlang/parser.py` — 递归下降解析器，AST 节点携带字节区间
- `modlang/ast_nodes.py` — AST 定义
- `modlang/interpreter.py` — 解释器与模块加载器（初始化图、缓存、别名绑定）
- `main.py` — CLI 入口
- `examples/` — 上述四个样例
- `tests/test_modlang.py` — unittest 测试
