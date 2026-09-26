# streamlex — 流式最长匹配词法器（纯标准库）

Python 3.11，仅标准库。**不使用任何正则或自动机库**：字母表、状态集合与转移表全部由调用方显式给出，构造时校验。

## 特性对照

| 需求 | 实现 |
| --- | --- |
| 最长匹配 + 规则优先级 | 所有活跃规则的 DFA 并行推进；`better_match` 先比长度再比 `priority`（小者胜） |
| 跨块不提前交付短 token | 只有当所有候选 DFA 都不可再延伸（或 `finish()`）时才交付 token |
| 词法状态切换 | 规则按 `states` 激活；动作 `Push/Pop/Switch` 操作状态栈 |
| 状态栈 / 撤销 / checkpoint | `state_stack`、`retract(n)`（撤销未消费尾部）、`pushback(text)`、`checkpoint()/restore()` |
| 同词素不同动作 | 同长同优先级不同规则 → `AmbiguousRuleError`；显式优先级选唯一动作 |
| 长失败前缀的缓冲上界 | 缓冲区恰为未判定前缀；超过 `max_buffer` 返回 `Status.INCOMPLETE`，不误切 token |
| 输入尾可能延伸 | 尾部 token 在 `finish()` 前不交付 |
| 无匹配精确偏移并可恢复 | `LexError(offset, char, state)`，`skip(n)` 后 `resume()` 继续 |
| 任意分块对照慢参考 | `tests/test_property_chunking.py` 用 300 组随机分块对照 O(n²) 参考 `streamlex.reference.lex_all` |

## 快速开始

```python
from streamlex import Lexer, Rule, literal, char_class_plus

rules = [
    Rule("KEY", literal("key"), priority=1, states={"S"}),
    Rule("ID", char_class_plus(set("abcdefghijklmnopqrstuvwxyz")), priority=2, states={"S"}),
    Rule("WS", char_class_plus({" "}), priority=3, states={"S"}, emit=False),
]
lx = Lexer(rules, alphabet=set("abcdefghijklmnopqrstuvwxyz "), initial_state="S")

lx.feed("ke")      # -> tokens=()        最长匹配未定，等待
lx.feed("y")       # -> tokens=()        "key" 仍可能延伸为标识符
lx.feed(" ")       # -> KEY 'key'        所有自动机死亡，判定
lx.feed("keyboard")
lx.finish()        # -> ID 'keyboard'    输入尾由 finish() 判定
```

## 判定规则与上界（可说明性）

- **交付条件**：从 token 起点并行推进各活跃规则的 DFA；当没有任何自动机处于"可延伸"状态（存在通往接受态的出边）时，最长匹配即确定。`finish()` 视为输入结束，强制判定。
- **缓冲上界**：缓冲区恰好保存"已消费但未交付"的前缀（当前未判定 token 起点之后的全部输入）。超过 `max_buffer` 时 `feed` 返回 `Status.INCOMPLETE`，缓冲保持完整；调大 `max_buffer` 继续喂，或 `finish()` 强制收尾——绝不提前切 token。
- **工作上界**：每个输入字符最多被每条活跃规则各扫描两次（越界预扫 + 重扫越界部分），总量 O(输入长度 × 活跃规则数)。

## 错误与恢复

```python
try:
    lx.feed("ab @cd")
except LexError as e:
    e.offset, e.char   # (3, '@')  精确偏移
    e.tokens           # 本次调用已产出的 token 不丢失
lx.skip(1)             # 丢弃出错字符
lx.resume()            # 继续扫描
```

## 目录

- `streamlex/dfa.py` — 显式 DFA（字母表/转移表校验，可延伸性预计算）
- `streamlex/rules.py` — 规则、优先级、`Push/Pop/Switch` 动作
- `streamlex/lexer.py` — 流式引擎（feed/finish/skip/retract/pushback/checkpoint）
- `streamlex/reference.py` — 慢速整段参考实现（性质测试对照用）
- `streamlex/builders.py` — 显式 DFA 构造辅助（`literal`、`char_class_plus`、`star_then_literal`）
- `tests/` — 42 个 unittest 用例
- `examples/` — 可独立执行的样例与反例

## 运行

```bash
python3.11 -m unittest discover -s tests -t . -v   # 测试
python3.11 examples/demo_streaming.py              # 流式分块演示
python3.11 examples/states_and_stack.py            # 状态栈演示
python3.11 examples/counterexample_premature_cut.py   # 反例：逐块定界误切
python3.11 examples/counterexample_buffer_limit.py    # 反例：缓冲超限不误切
```

## 测试结果（python3.11）

```
Ran 42 tests in 0.132s
OK
```

## 样例输出

`examples/counterexample_premature_cut.py`：

```
chunks: ['ke', 'y ']
naive per-chunk lexer : [('ID', 'ke'), ('ID', 'y')]   <-- WRONG, keyword split
streamlex             : [('KEY', 'key')]
OK: streamlex held the token until the longest match was decided
```

`examples/counterexample_buffer_limit.py`：

```
after 100 x 'a': status=incomplete, tokens=[], buffered=100
after 'b':      status=ok, tokens=[('AB', 101)]
OK: INCOMPLETE was reported instead of mis-cutting the token
```

`examples/demo_streaming.py`（输入 `'let total == total1 + 42;'`，每 7 字符一块）：

```
  chunk 'let tot' -> LET  'let' [0:3]
  chunk 'al == t' -> ID   'total' [4:9]
  chunk 'al == t' -> EQEQ '==' [10:12]
  chunk 'otal1 +' -> ID   'total' [13:18]
  chunk 'otal1 +' -> NUM  '1' [18:19]
  chunk    ' 42;' -> OP   '+' [20:21]
  chunk    ' 42;' -> NUM  '42' [22:24]
     finish() -> OP   ';' [24:25]
```
