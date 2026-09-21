# macro_lang:基于语法树的卫生参数化宏(Python 3.11,纯标准库)

从零实现的小型 s-expression 语言:手写词法/语法分析、语法树宏展开器、
树遍历解释器。不使用 `eval`/`compile` 或任何现成编译器,仅依赖标准库,
测试基于 `unittest`。

## 特性

- **参数化宏**:`(defmacro name (params...) body)`,按语法树展开,实参以
  AST 形式代入模板。
- **卫生(hygiene)**:宏模板中 `let` 引入的局部绑定自动重命名为
  `名字#N`(gensym),不会捕获调用方同名变量;宏参数引用按作用域解析,
  不会被无关重命名波及。
- **双重源码映射**:每个展开产物节点同时记录宏定义处与宏调用处的位置
  (`Origin.def_span` / `Origin.call_span`),`Expander.source_map` 汇总。
- **UTF-8 字节区间**:所有源码位置均为 `[start, end)` 字节偏移,非 ASCII
  源码(注释、字符串)同样精确。
- **展开限制**:展开深度(`max_depth`,默认 64)与展开节点数
  (`max_nodes`,默认 20000)双上限;递归宏超限时报出完整展开链
  (宏名 + 调用处字节区间 + 定义处字节区间)。

## 语言速览

```lisp
; 注释以分号开头
(defmacro swap! (a b)          ; 定义宏
  (let ((tmp a))
    (do (set a b) (set b tmp))))

(let ((x 1) (y 2) (tmp 99))    ; let 顺序绑定(类似 let*)
  (swap! x y)                  ; 宏调用
  (print x y tmp))             ; => 2 1 99(调用方 tmp 未被覆盖)
```

特殊形式:`defmacro`、`let`、`set`、`if`、`while`、`do`、`and`、`or`。
内建函数:`+ - * / % = != < <= > >= not print`;字面量:整数、浮点数、
字符串、`true`/`false`/`nil`。

## 运行

```bash
python3.11 demo.py                 # 运行样例(卫生、限制、源码映射)
python3.11 -m unittest discover    # 运行测试(23 个用例)
```

## 运行样例输出(节选)

```
1. 卫生宏:调用方的 tmp 不被宏内 tmp 覆盖
2 1 99
2. 引用参数不被无关重命名
-> (plus-one tmp) = 42(实参 tmp 仍指调用方的 41,而非宏内的 1000)
3. 展开结果(可见卫生重命名 tmp#N)
(let ((tmp#1 7)) (+ tmp#1 tmp#1))
4. 递归宏达到深度上限:报告完整展开链
macro expansion depth limit (6) exceeded
expansion chain (outermost first):
  1. macro 'boom' called at bytes 36-44 (defined at bytes 0-35)
  2. macro 'boom' called at bytes 19-34 (defined at bytes 0-35)
  ...
5. 节点数上限:指数膨胀被拦截
默认限制下 (dup^7 1) = 128
macro expansion node-count limit (50) exceeded
6. 双重源码映射:定义处 + 调用处(UTF-8 字节区间)
节点    宏     定义处字节区间   调用处字节区间   定义处源码
tmp#1   swap!  102-105         173-184         'tmp'
```

## 测试结果

```
$ python3.11 -m unittest discover
.......................
----------------------------------------------------------------------
Ran 23 tests in 0.004s

OK
```

## 代码结构

- `macro_lang/lexer.py` — 词法分析,token 携带 UTF-8 字节区间
- `macro_lang/parser.py` — 递归下降语法分析,产出 AST
- `macro_lang/ast_nodes.py` — AST 节点、`Span`/`Origin`、渲染与克隆
- `macro_lang/expand.py` — 宏展开器:卫生重命名、参数代入、深度/节点限制、
  展开链与双重源码映射
- `macro_lang/interp.py` — 树遍历解释器(作用域环境 + 内建函数)
- `macro_lang/api.py` — `expand_source` / `run_source` 入口
- `tests/test_macro_lang.py` — 23 个 unittest 用例
- `demo.py` — 可运行样例

## 卫生算法要点

展开 `(swap! x y)` 时:

1. 形参 `a`/`b` 绑定到实参 AST(保留其调用处 origin);
2. 遍历模板,`let` 绑定名生成新鲜名 `tmp#1`,只重命名被该 `let`
   约束的引用(作用域感知);
3. 形参引用优先于局部重命名解析,直接替换为实参 AST 的深拷贝——
   因此实参里的 `tmp` 不会被改写成 `tmp#1`;
4. 模板中生成的每个节点登记 `(定义处字节区间, 调用处字节区间)`;
5. 代入结果继续递归展开(支持宏套宏),每深入一层记录一帧展开链,
   深度或节点数超限即抛出携带完整链的 `ExpansionLimitError`。
