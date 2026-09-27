# fsmcheck — 有限状态模型死锁检查器

Python 3.11，仅标准库，测试用 unittest。对给定有限状态模型做死锁分析，
区分**声明终态**与**无可执行转移的异常死锁**，输出阻塞动作及其前置条件，
支持多初态、最短可重放反例、预算未决与独立完整转移图核验。

## 模型与规则

- 模型由若干参与者（各自带局部转移）和动作组成；动作需**全部**列出的参与者
  同步提供，局部可用不等于全局可执行。
- 全局状态 = 各参与者局部状态的元组；支持多个初态，BFS 全部覆盖。
- 状态分类：
  - 声明终态：按明确规则 `declared-terminal-states-are-not-expanded` 不展开；
    正常终态（无边）不报死锁。若声明终态仍有可执行动作，给出
    `terminal-has-actions` 警告而非死锁。
  - 非终态且无任何全局可执行动作：**死锁**，输出最短反例与诊断。
  - 非终态但所有可执行动作只回到自身：**活锁候选**警告，不算死锁。
- 预算（budget）限制展开数；预算耗尽且前沿非空时结果为 `pending`，
  尚未展开不等于终止，核验保持未决（inconclusive）。
- 死锁诊断：列出每个被阻塞动作的提供者与缺失者及其前置条件；
  由“等待”关系给出最小阻塞依赖环，或当某参与者不提供任何动作时给出
  无提供者（no-provider）证据。
- 独立核验器（`fsmcheck/verifier.py`）独立重枚举完整转移图：重放反例逐步
  校验、确认终态确为死锁、确认证据长度为所有初态出发的最短距离、
  确认所有初态均被覆盖。搜索未完成时结论保持未决。

## 运行

```sh
python3.11 samples.py      # 运行样例
python3.11 -m unittest discover -s tests -v   # 运行测试
```

## 运行样例（`python3.11 samples.py` 节选）

```
=== circular wait (minimal blocking cycle) ===
status: deadlock (expanded=1)
initial: ('a0', 'b0')
deadlock state: ('a0', 'b0')
  blocked action: ab; missing: ['B']
    precondition: A must be in a state offering 'ab' (currently offers ['a0->a1'])
  blocked action: ba; missing: ['A']
    precondition: B must be in a state offering 'ba' (currently offers ['b0->b1'])
  minimal blocking cycle: A -> B -> A
verification: conclusive: True, ok: True, is-shortest: True, replay-valid: True

=== no provider, multi-initial shortest evidence ===
status: deadlock (expanded=2)
initial: ('waiting', 'down')          # 两个初态中距离最短的反例
  blocked action: request; missing: ['Client', 'Server']
  blocked action: reply; missing: ['Server']
  no-provider participants: ['Server']

=== declared terminal with actions ===
status: no-deadlock (expanded=2)
warning: terminal-has-actions: declared terminal ('done',) still has executable
actions; rule 'declared-terminal-states-are-not-expanded' applies

=== self-loop livelock candidate ===
status: no-deadlock (expanded=2)
warning: livelock-candidate: ('m1',) only reaches itself via ['spin']

=== clean model, normal terminal ===
status: no-deadlock (expanded=1)      # 正常终态无边，不报死锁

=== budget pending (unexpanded frontier is not termination) ===
status: pending (expanded=2)
pending frontier: [('s2',)]
verification: conclusive: False, note: budget exhausted; unexpanded frontier is not termination
```

## 测试结果

`python3.11 -m unittest discover -s tests`：15 个测试全部通过（OK），覆盖：

- 声明终态 vs 异常死锁的区分；声明终态仍有动作时的明确规则与警告
- 多参与者同步：局部可用不等于全局可执行；阻塞动作与前置条件输出
- 最小阻塞依赖环与无提供者证据
- 多初态覆盖与跨初态最短反例；反例可重放且拒绝非法步骤
- 预算未决（pending 不当终止；未决结果核验 inconclusive）
- 自环为活锁候选而非死锁；正常终态无边不报死锁
- 独立完整转移图核验（重放、终态死锁确认、最短性、初态覆盖）

```
Ran 15 tests in 0.001s

OK
```

## 文件

- `fsmcheck/model.py` — 模型定义与全局后继枚举（同步语义）
- `fsmcheck/checker.py` — 多源 BFS 检查、预算、死锁诊断（最小环/无提供者）
- `fsmcheck/verifier.py` — 独立转移枚举、反例重放、完整图核验
- `samples.py` — 六组运行样例
- `tests/test_checker.py` — unittest 测试
