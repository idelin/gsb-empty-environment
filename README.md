# minilang — 带最小编辑成本错误恢复的声明式小语言

纯 Python 3.11 标准库实现（词法、语法、解释执行全部手写，不使用 `eval`
或任何现成编译器组件），测试使用 `unittest`。

## 语言

```
program := decl*
decl    := "let" IDENT "=" expr ";"
expr    := term (("+"|"-") term)*
term    := factor (("*"|"/") factor)*
factor  := NUMBER | STRING | IDENT | "(" expr ")"
```

`#` 为行注释。所有源码位置均为 UTF-8 字节区间（`start..end`）。

## 错误恢复设计

- **声明列表同步点**：程序按声明解析；声明间的游离 token 被归并为一条
  诊断并在下一个 `let`/`;`/EOF 处停下，合法后续声明不会被吞掉。
- **最小编辑成本**：恢复是对编辑序列（插入虚拟 token / 删除 token /
  替换为虚拟 token，各计成本 1）的按层 BFS。首个能完整解析当前声明的
  成本层即为可证最小成本；编辑可作用于失败点或本声明内已消费的 token
  （替换较早的 token 可能严格更优），失败点之后的编辑可延后，不影响
  最小性。绝不把贪心同步说成最小。
- **候选枚举与截断**：预算内返回同一最小成本的全部候选；超出
  `max_candidates` / `max_states` / `max_cost` 预算时显式标记
  `truncated` 或报告"预算内无法恢复"，绝不假装完整。
- **token 身份**：AST 节点引用原 token 对象；恢复引入的 token 带
  `virtual=True` 标记（负 `token_id`），原 token 身份不变。
- **局部编辑**：`Document.edit(start_byte, end_byte, replacement)` 以
  UTF-8 字节区间应用编辑并刷新诊断、语法树与环境。
- **级联噪声控制**：未闭合字符串只报一条诊断（不逐字符报错）；连续非法
  字符归并为一条；每个独立语法错误只报一条，错误区间不跨越无关合法声明。

## 运行样例

```
$ python3.11 demo.py
== source ==
let width = 8;
let height = 5
let area = width * height;
let label = "area=" + "cm2";
let broken = ((width + 2) * 3;
let tail = "unterminated
let after = 42;

== diagnostics ==
[lexer] bytes 128..141: unterminated string literal
[parser] bytes 29..30: expected SEMI before 'let'; recovered with minimal edit cost 1 (all 2 minimal fix candidate(s) listed)
    fix: insert SEMI at byte 30
    fix: replace 'let' at bytes 30..33 with SEMI
[parser] bytes 115..115: expected RPAREN before ';'; recovered with minimal edit cost 1 (all 3 minimal fix candidate(s) listed)
    fix: insert RPAREN at byte 115
    fix: delete '(' at bytes 99..100
    fix: delete '(' at bytes 100..101
[parser] bytes 141..142: expected SEMI before 'let'; recovered with minimal edit cost 1 (all 2 minimal fix candidate(s) listed)
    fix: insert SEMI at byte 142
    fix: replace 'let' at bytes 142..145 with SEMI
== environment after execution ==
  width = 8
  height = 5
  area = 40
  label = 'area=cm2'
  broken = 30
  tail = 'unterminated'
  after = 42
```

两处缺分号各报一条；未闭合字符串只报一条词法诊断；嵌套缺括号恢复后
`broken` 与后续 `after` 均正常求值。

## 测试结果

```
$ python3.11 -m unittest discover -s tests -v
...
Ran 18 tests in 0.620s
OK
```

覆盖：短输入穷举编辑序列核验最小成本（独立的全编辑空间 BFS 对照）、同
成本候选全枚举与显式截断、嵌套缺括号、合法后续声明不被吞掉、两处缺分号
各报一条、未闭合字符串单条诊断、虚拟 token 标记与原 token 身份、恢复后
声明可执行、UTF-8 字节区间、局部编辑后诊断更新、预算内不可恢复时的显式
报告。

## 结构

- `minilang/tokens.py` — token 定义与虚拟 token 工厂
- `minilang/lexer.py` — 手写词法器（UTF-8 字节区间）
- `minilang/parser.py` — 递归下降解析器 + 最小编辑成本恢复搜索
- `minilang/interpreter.py` — 树遍历解释器
- `minilang/document.py` — 支持局部编辑的文档接口
- `tests/test_recovery.py` — unittest 测试套件
- `demo.py` — 运行样例
