# parserlib — 预测表解析、错误恢复与预算修复

Python 3.11，仅标准库。基于显式无冲突 LL(1) 预测表解析 token 序列并构建
语法树；错误恢复采用 FOLLOW 同步；修复引擎在预算内求最小 token 编辑成本，
并以共享森林返回全部同成本候选。

## 运行

```sh
python3.11 -m unittest discover -s tests -t . -v   # 全部测试（56 个）
python3.11 examples/demo.py                        # 端到端运行样例
python3.11 counterexamples/ce_01_follow_sync.py    # 反例均可独立执行
```

## 需求 → 实现映射

- **无冲突预测表解析建树**：`parserlib/table.py` 构建 LL(1) 表，任何单元
  冲突抛 `TableConflictError`；`parserlib/parser.py` 表驱动展开并构建
  `parserlib/tree.py` 的 `Node` 树。
- **FOLLOW 同步恢复**：表缺失时按 FOLLOW 同步弹栈；可空非终结符仅当剩余
  栈"可行"（`_viable`）时按 ε 静默弹出；跳过的 token 保留在 `<error>`
  节点内（`Node.skipped`），同步在首个可继续合法结构的 lookahead 处停止，
  不吞掉后续合法结构。
- **预算内最小编辑成本修复**：`parserlib/repair.py` 在 (位置, 栈) 状态空间
  上跑 Dijkstra（插入/删除/替换均 cost 1，预算剪枝），取最小成本；紧边
  子图构成共享候选森林（DAG），所有同成本修复全部返回，不任意装作唯一。
- **token 身份与源码映射**：`parserlib/tokens.py` 每个 token 有唯一 `uid`
  与 `Origin`（ORIGINAL/INSERTED/SUBSTITUTED/EOF）；插入 token 映射到插入
  点的零宽 span，原 token 身份与 span 在恢复后保留（`Node.source_map()`）。
- **短串枚举编辑参考核验**：`parserlib/reference.py` 对短串 BFS 枚举全部
  编辑脚本；`tests/test_reference.py` 在短串空间上逐条交叉验证引擎的
  最小成本与候选集合。
- **增量修改只失效相关块**：`parserlib/incremental.py` 按 `;` 分块、以
  token 身份序列为缓存键；单 token 编辑只重解析受影响的块，块内恢复不会
  波及后续合法块。
- **显式字母表与转移、无现成库**：`parserlib/automata.py` 手写 NFA/DFA
  （ε 闭包、子集构造、最长匹配词法器），字母表与转移关系全部显式输入；
  全包不 import `re` 或任何自动机库（`tests/test_automata.py` 用 AST 元
  测试强制）。
- **反例可独立执行**：`counterexamples/ce_*.py` 各自带 `__main__`，直接
  `python3.11 counterexamples/ce_XX_*.py` 运行，退出码非 0 即失败。
- **缺终结符定位期望集合**：诊断携带排序后的期望集合
  （`missing-terminal` / `unexpected-token`），精确到 token 位置与源码偏移。
- **空产生式正确建树**：ε 产生式生成显式 `ε` 叶节点（空程序、表达式尾
  均有测试覆盖）。
- **多处错误不无限弹栈、不重复报同位置**：每次迭代必消耗输入或弹栈（另
  有硬步数上界兜底）；诊断按 token 位置去重。

## 目录

```
parserlib/      核心库（tokens/errors/grammar/table/tree/parser/repair/
                reference/automata/demolang/incremental）
tests/          unittest 套件（含参考核验与反例子进程测试）
counterexamples/ 可独立执行的反例脚本
examples/demo.py 词法→解析→恢复→修复→增量 全流程样例
```
