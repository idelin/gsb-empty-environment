# regexlite

一个自包含的小型正则引擎（Python 3.11+，仅标准库，**不使用任何现成正则或自动机库**）。
支持连接、有序并（`|`）、贪婪星号（`*`）、字符类（`[...]` / `[^...]`）、括号与
**带标签捕获**（`(?<name>...)`、`(?P<name>...)`、自动编号的 `(...)`）。

## 架构

```
pattern ──parse──▶ AST(带源码span) ──compile──▶ 带标签 ε-NFA(显式字母表)
                                                │
              ┌─────────────────────────────────┤
              ▼                                 ▼
     Determinizer(按需子集构造)          Pike VM(优先级线程模拟)
     LRU缓存 + 硬状态预算                捕获标签的唯一权威来源
              │                                 ▲
              └──── DFA 定界(最长匹配范围) ──────┘
                    捕获由 Pike VM 在命中区间上重放获得
```

- `regexlite/parser.py` — 递归下降解析；所有 AST 节点携带 `span=(start,end)`，错误带偏移量。
- `regexlite/nfa.py` — Thompson 构造。字母表是**显式输入**；转移按符号逐条存储；
  捕获边界是 ε 边上的标签操作（组 `i` 用槽位 `2i`/`2i+1`）；每个 NFA 状态记录其源码 span。
- `regexlite/pike.py` — Pike VM。线程按优先级排序；同位置同状态合并时**胜者（高优先级）
  线程的标签被保留**，低优先级线程被丢弃——这就是确定优先规则，标签不会因合并丢失。
  每位置的 visited 集合切断 ε 环，嵌套星号 `(a*)*` 不会死循环。
- `regexlite/dfa.py` — 惰性确定化。两个独立额度：`cache_size`（LRU，驱逐只导致重算，
  **不改变语义**）与 `state_budget`（不同子集总数的硬上限，超出即抛 `BudgetExceeded`
  并把状态置为 `incomplete`——**报告未完成，而不是返回被截断的错误自动机**）。
- `regexlite/matcher.py` — `Regex` 批量 API 与 `StreamMatcher` 分块流式匹配。
  预算不足且允许回退时整体退回 Pike VM（结果仍正确，`Match.engine='pike'`）。
- `regexlite/reference.py` — **独立**回溯参考实现（不共享匹配代码），供对拍。
- `regexlite/fuzz.py` — 穷举小字符串对拍 + 反例复现脚本生成。

## 匹配语义（确定优先规则）

1. **最左最长**：`search` 返回最小起点；同起点取最长终点。
2. 同长度的多条路径中，**优先级最高者**提供捕获：并按书写顺序、星号贪婪
   （优先再迭代一次）。
3. **空迭代禁止**：星号体匹配空串时该次迭代被跳过（循环终止），与 Pike VM 的
   ε 环切割一致；参考回溯器采用同一规则，因此 `(a*)*` 等嵌套星号必然终止。
4. `()` / 空模式匹配空串；`[]` 是**空语言**，什么都不匹配——两者严格区分。
5. 输入中字母表之外的字符视为"无转移"（线程死亡），不会报错。

## 流式匹配与最长匹配等待

`rx.stream()` 返回 `StreamMatcher`：`feed(chunk)` 增量喂入，`finish()` 表示输入结束。
由于语义是最长匹配，候选匹配只有在**不可能再延长**时才发出：线程全部死亡、
存活线程没有任何出边、或 `finish()` 到达 EOF。否则匹配器等待更多输入。
每个匹配发出后自动机在匹配终点（空匹配则 +1）重启，扫描在流内恢复继续。
分块结果与一次性 `finditer` 完全一致（有随机分块对拍测试保证）。

## 快速开始

```python
from regexlite import Regex

rx = Regex.compile("(?<key>[a-z]*)=(?<val>[0-9]*)",
                   alphabet="abcdefghijklmnopqrstuvwxyz0123456789=")
m = rx.search("xx foo=123 y")
m.span            # (3, 10)
m.group("key")    # 'foo'
m.captures["val"] # (7, 10)

st = rx.stream()          # 分块输入
events = st.feed("foo=")  # -> []（最长匹配等待中）
events = st.feed("123")
events += st.finish()     # -> [Match(0, 7, ...)]
```

运行样例：`python3 demo.py`（解析树/源码位置、NFA 统计、捕获、优先级、
空串 vs 空语言、嵌套星号、流式等待、预算回退、驱逐一致性、穷举对拍）。

## 测试

```
python3 -m unittest discover -s tests -v
```

- `tests/test_parser.py` — 优先级、span、类语法、标签、错误位置（28 例）
- `tests/test_pike.py` — 匹配、最长/优先、捕获边界、空串 vs 空语言、
  嵌套星号终止、字母表校验、NFA 源码位置保留（27 例）
- `tests/test_dfa.py` — DFA 与 Pike 一致性、按需构造、预算 `incomplete`、
  回退正确性、**缓存驱逐不改变结果**（9 例）
- `tests/test_stream.py` — 最长匹配等待、分块等价、空匹配推进、恢复状态（11 例）
- `tests/test_fuzz.py` — 28 个刁钻模式穷举对拍（≤5 长）、60 个随机模式穷举
  （≤4 长）、流式==批量随机对拍、**反例复现脚本独立执行**（10 例）

最近一次运行：**84 tests, OK**（Python 3.13；代码兼容 3.11）。

## 反例复现

对拍发现不一致时，`regexlite.fuzz.write_reproducer` 生成一个**独立可执行**
的 Python 脚本（内嵌模式、字母表、输入与双方记录结果），`python3 repro.py`
退出码非零即复现。测试中用子进程验证了两种退出路径。
