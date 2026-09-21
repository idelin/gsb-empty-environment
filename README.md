# macrolang：基于语法树的卫生参数化宏（Python 3.11，仅标准库）

从空仓库实现的一个小型语言：自带词法/语法分析、卫生宏展开器与树遍历解释器。
全程不使用 `eval`/`exec`/`compile` 或任何现成编译器；所有源码位置均为
**UTF-8 字节区间** `[start, end)`。

## 语言速览

```lisp
(def tmp 100)          ; 定义变量
(defmacro swap (a b)   ; 定义参数化宏
  (let tmp a           ; 宏内局部绑定：自动卫生改名，不捕获调用方 tmp
    (seq (set a b) (set b tmp))))
(swap tmp y)           ; 实参 tmp 指调用方变量，不会被宏内重命名波及
```

特殊形式：`def` `set` `let` `seq` `if` `while` `print` `defmacro`；
内建运算：`+ - * / %` `< <= > >= =` `not`。注释以 `;` 到行尾。

## 设计要点

- **卫生（防捕获）**：宏模板中 `let` 引入的绑定一律改名为 `名字#序号`。
  `#` 被词法器保留，用户源码不可书写，因此新名不可伪造；实参语法树原样
  深拷贝移植，绝不参与模板内的重命名。
- **双重源码映射**：每个由模板生成的节点都携带 `ExpansionRecord`
  （宏名、调用点字节区间、定义点字节区间），嵌套展开时外层记录保留在前。
- **资源限制**：展开深度（默认 64）与产生节点数（默认 100000）均有上限；
  超限抛出 `DepthLimitExceeded` / `NodeLimitExceeded`，错误信息附带
  完整展开链（宏名 + 各层调用点字节区间）。

## 目录结构

- `macrolang/lexer.py` — 词法分析，产出带 UTF-8 字节区间的 token
- `macrolang/parser.py` — 语法分析，产出通用语法树
- `macrolang/expander.py` — 卫生宏展开器（改名、双重映射、限制、展开链）
- `macrolang/interpreter.py` — 树遍历解释器（词法作用域）
- `macrolang/nodes.py` / `macrolang/errors.py` — 节点/错误定义
- `examples/sample.macro` + `run_sample.py` — 运行样例
- `tests/test_macrolang.py` — unittest 测试（25 例）

## 运行

```sh
python3.11 run_sample.py                       # 样例：输出 + 源码映射 + 超限演示
python3.11 -m unittest discover -s tests -v    # 测试
```
