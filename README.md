# 确定性 Mealy 机转移覆盖自适应测试规划器

仅使用 Python 3.11 标准库实现，不依赖自动机、规划或搜索第三方库。代码位于 `mealy_planner/`，样例位于 `examples/`。

## 建模语义

输入 JSON 字段：

- `states`、`inputs`、`outputs`：互不重复的符号集合。
- `initial_candidates`：设备可能的真实初态；真实初态全程固定，测试者只能看到输出。
- `resettable`：当前真实状态在其中时 reset 才合法。
- `input_costs`：每个输入的正整数成本；`reset_cost` 是一次 reset 的正整数成本。
- `transitions`：确定性部分 Mealy 转移，每条为 `{source,input,output,target}`；边 ID 固定为 `source:input`。

需要覆盖的转移是从任意初始候选经普通输入可达的全部边。不可达边自动排除。

本实现采用 reset 前的真实测试语义：

1. 每个可能世界保存真实初态 `origin`、当前状态和该轨迹已执行的边。
2. reset 只有在当前信念中的所有可能当前状态都属于 `resettable` 时才安全。
3. reset 后每个可能世界回到自己的真实初态 `origin`，reset 不产生输出，也不覆盖转移。
4. reset 前已经覆盖的边继续保留，并计入后续“确定覆盖”。

确定覆盖不是覆盖集合的并。一个观测分支中的信念包含若干可能实际轨迹；只有所有轨迹都执行过同一条边，该边才进入公共覆盖集合。输出相同但候选分别走了不同内部边时，这些边仍只是各轨迹的私有覆盖，不能合并。

## 使用方法

```bash
python -m mealy_planner.cli plan examples/same_output_no_reset.json
python -m mealy_planner.cli verify examples/same_output_no_reset.json outputs/same_output_no_reset.json
python -m mealy_planner.cli brute-check examples/same_output_no_reset.json --cost 4
python -m unittest discover -s tests
```

`plan --budget N` 的预算单位是“展开的增广信念节点数”；`N=0` 允许只使用初始启发式下界。完整展开有限增广状态图后，规划器才可能返回 `unsat`；预算耗尽时一律返回 `undecided`。

输出状态：

- `solved`：完整搜索证明最优，给出策略树和最坏成本。
- `undecided`：预算不足；返回有效下界，若已构造出可行树则同时返回上界和策略。
- `unsat`：完整展开闭包后证明无解，并给出阻塞信念、不安全输入、非法 reset 状态、不可完成世界和失败动作见证。

策略树节点只依赖观测历史：

- `input` 节点包含输入和每个可能输出的后继。
- `reset` 节点只有一个后继，并显式记录 reset 成本。
- `terminal` 节点无后继。
- `belief_audit` 只是审计信息；独立验证器不会信任它。

## 精确 AND/OR 搜索

增广状态为：

```text
B = (C, {(origin_i, state_i, P_i)})
```

其中 `C` 是所有可能轨迹公共覆盖的边，`P_i` 是轨迹 `i` 已覆盖但尚未公共的私有边。完整轨迹覆盖集是 `C ∪ P_i`。

- OR：测试者在当前信念选择一个所有候选都定义的输入，或选择对所有候选当前状态都合法的 reset。
- AND：输入后设备实际输出分支；策略必须让每个输出分支最终到达目标。
- 边值：动作成本加所有输出子节点值的最大值。
- 节点值：所有合法动作值的最小值。
- reset 是无分支单后继动作，同样计入成本。

状态有限，因为候选世界数、状态数和覆盖位掩码都有限。搜索会规范化并合并相同增广信念，但最终策略会按历史展开成树，避免把不同观测历史强行合并。

初始下界来自逐世界的 `(状态, 剩余边掩码)` Dijkstra。它忽略观测和同步限制，只问某一条真实轨迹单独是否可能补齐剩余边，因此是可采纳下界；任一世界不可能补齐时，该信念永久失败。

## 最优性、无解和终止

上界传播从目标节点开始，按正成本的 AND/OR 标号设置算法进行：

1. 目标节点值为 0。
2. 一个动作只有在所有输出子节点都得到可行值时才有值，值为动作成本加最大子值。
3. 节点取所有动作最小值。
4. 所有动作成本为正，所以通过环返回同一有限成本不能产生更小标号；最优策略可展开为无环树。

完整展开后，所有不能得到有限值的节点构成失败闭包：其中每个非目标节点的每个合法动作都至少有一个输出后继仍在失败集合中。这给出了无解的结构证明，而不是“搜索没找到”。

验证器额外拒绝策略环。因此不要求验证器假设“无限循环最终会覆盖”；输出策略的终止由正成本、严格下降的有限标号和策略树无环共同保证。

## 状态空间削减

实现使用以下削减，不改变最优值：

1. 只保存从初始信念经安全动作可达的增广信念。
2. 规范化公共覆盖 `C`、私有覆盖 `P_i` 和候选世界；相同 `(origin,current,P)` 世界去重。
3. 永久失败的单轨迹节点不展开，只作为失败闭包中的目标。
4. 跳过回到完全相同增广信念的自环动作；正成本使这种动作不可能改善最优值。
5. 搜索图共享相同增广状态，输出时才复制成观测历史策略树。
6. 单轨迹 Dijkstra 下界按 `(origin,current,missing-mask)` 缓存，用于尽早识别永久失败。

## 样例与反例

| 文件 | 场景 | 完整搜索结果 |
|---|---|---:|
| `examples/same_output_no_reset.json` | 输出全相同，但候选经过不同内部边 | 最优 4 |
| `examples/identify_then_act.json` | 必须先用 `p` 识别，再分别选择 `x`/`y` | 最优 6 |
| `examples/reset_partial_solvable.json` | 只有部分状态能 reset，最优分支需要 reset | 最优 7 |
| `examples/unsafe_indistinguishable_unsat.json` | 可识别的探针后，区分动作对一个候选未定义 | 无解 |
| `examples/sink_loss_unsat.json` | 一个输出分支进入不可逆汇点，永久丢失边 | 无解 |

关键反例：

- 在同输出样例中第一次执行 `a` 后，两个候选分别只覆盖 `s0:a` 和 `s1:a`；虽然观察到的输出相同，公共覆盖仍为空。
- 在识别样例中，未识别时执行 `x` 对一个候选安全、对另一个候选未定义；策略必须先观察 `p` 的输出。
- 在部分 reset 样例中，初始信念包含不可 reset 状态，所以初始 reset 非法；观察到输出 `0` 后才到达可 reset 的 `t0`。
- 在汇点样例中，`s0 --p/0--> z` 后只能留在 `z`，该输出分支不可能覆盖从 `s1` 可达的边；完整失败闭包证明无解。
- `--budget 0` 对可解机器返回 `undecided` 和启发式下界，不会因为没有搜索而声称无解。

## 独立验证器

`mealy_planner/verifier.py` 直接重新读取机器和策略 JSON，不使用规划器的搜索结论。它会：

1. 检查策略 JSON 结构并证明策略图无环。
2. 对每个可能真实初态独立模拟完整轨迹。
3. 在每个上下文重新计算当前可能世界集合，检查输入是否对所有候选都有定义。
4. 检查 reset 前的每个可能当前状态都在 `resettable` 中。
5. 检查每个输出分支都存在，拒绝未声明的观察输出。
6. 分别累计每个真实初态的输入和 reset 成本。
7. 在每个终止点确认所有要求转移都被该真实轨迹执行过。
8. 计算各初态成本的最大值，作为策略最坏分支成本。

穷举核验器 `mealy_planner/brute.py` 独立枚举小成本策略树。它用 `feasible(belief, remaining_budget)` 记忆化递归：OR 枚举动作，AND 要求所有输出分支可行。对规划器声称值 `V`，分别检查成本上界 `V` 和 `V-1`，由此证明最优值；正整数成本保证深度不会超过成本。

## 实测结果

使用 Python 3.13 运行（代码仅使用 3.11 即可提供的标准库特性）：

```text
same_output_no_reset       solved, worst cost = 4, expanded nodes = 12
identify_then_act          solved, worst cost = 6, expanded nodes = 21
reset_partial_solvable     solved, worst cost = 7, expanded nodes = 22
unsafe_indistinguishable   unsat, losing region closed
sink_loss                  unsat, losing region closed
```

穷举核验结果：

```text
cost 4/3 for same_output_no_reset:       feasible=true/false
cost 6/5 for identify_then_act:          feasible=true/false
cost 7/6 for reset_partial_solvable:     feasible=true/false
```

自动化测试共 11 项，覆盖确定覆盖、非法 reset、无解闭包、预算未决、策略环和不安全动作拒绝。
