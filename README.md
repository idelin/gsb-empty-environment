# minitms — 最小支持集合真值维护（合成数据，证据可重放）

Python 3.11 + 标准库 + `unittest`，无第三方依赖。

## 能力

- **合取前提**：规则 `A & B => C` 只有所有前提同时成立才触发。
- **多条替代规则**：同一结论可由多条规则独立支撑，标签保留所有极小支持环境。
- **撤回与最小支持集维护**：撤回外部事实后重算不动点；包含该事实的支持环境全部消失；
  只要还剩任一环境，结论保留；全部消失才撤回结论。
- **无外部支撑的循环不成立**：`X=>Y, Y=>X` 在没有外部事实时不产生任何环境，
  X、Y 均不成立（环境只从活动前提事实增长，纯循环永远得不到非空环境）。
- **稳定 ID 与版本**：事实、规则、步骤（一规则一步，步骤 ID = 规则 ID）都有稳定 ID。
  - 事实：创建 `version=1`，每次 active↔inactive 转换 +1；重复断言同一事实 ID 为空操作。
  - 规则：不可变，`version=1`，重复 ID 报错。
  - 步骤：不可触发时 `version=0`，首次触发为 1，之后每次 active/inactive 翻转 +1。
- **重复事实不制造虚假支持数**：支持数是极小环境集合的基数（集合，不去计重复次数）。
  重复断言同一事实 ID 不改变任何标签；两个不同事实 ID 断言同一原子会产生两个独立极小环境，
  撤回其中一个不影响另一个。
- **证据可重放**：所有有效变更以追加事件日志记录（含 ID、版本），
  `TruthMaintenanceSystem.from_evidence()` 可重建出逐位一致的状态快照。

## 设计要点（ATMS 风格标签）

- 环境 = 外部事实 ID 的 `frozenset`；空环境表示公理（无前提规则）。
- 每个已相信原子带一个标签：其包含序意义下极小的环境反链。
- 规则触发取各前提环境的笛卡尔积并集；每轮用 `minimize_envs` 剪去超集环境。
- 标签在有限的前提 ID 幂集格上单调增长，故不动点必然终止；解释树（witness）在收敛后
  依据最终标签确定性重建。
- 步骤是否 active = 该规则能否产出最终极小标签中的某个环境。

## 运行

```bash
python3.11 -m minitms.demo                 # 运行样例
python3.11 -m unittest discover -s tests   # 运行测试
```

## 样例关键输出

规则 `A,B=>C` 与 `D=>C`，事实 `fA=A, fB=B, fD=D`：

```
--- after asserting A, B, D: C has two independent supports
  C: BELIEVED minimal_supports=[['fD'], ['fA', 'fB']] count=2
  steps: r_ab_implies_c:on@v1, r_d_implies_c:on@v1, r_x_implies_y:off@v0, r_y_implies_x:off@v0
  repeat assert fB -> C support count: 2 (still 2, fact fB version: 1 )
--- retract B: D still supports C, conjunctive step stops
  C: BELIEVED minimal_supports=[['fD']] count=1
  steps: r_ab_implies_c:off@v2, r_d_implies_c:on@v1, ...
--- retract D too: no support left, C withdrawn
  C: absent   minimal_supports=[] count=0
  steps: r_ab_implies_c:off@v2, r_d_implies_c:off@v2, ...
--- ungrounded cycle X->Y->X: X = False , Y = False (both must be False)
--- replay: snapshot identical -> True | C believed after replay: False
```

完整输出（含证据日志 JSON）见 `demo_output.txt`。

## 测试结果

`python3.11 -m unittest discover -s tests`（24 个用例）：

```
Ran 24 tests in 0.001s
OK
```

覆盖：合取前提、替代规则、撤 B 保 C、再撤 D 撤 C、重复事实不增支持数、
纯循环与自循环无支撑、循环落地后撤回种子、稳定 ID/版本翻转、超集环境裁剪、
公理空环境、链式推导、解释树、证据重放一致性与坏版本报错。
