# simcheck — 有限状态机模拟关系检查器

检查**实现机是否被规格机模拟**（simulation），仅接受显式给出的**有限状态**
LTS 模型。支持**弱模拟（weak simulation）**与**分支模拟（branching
simulation）**两种语义，内部动作为 `tau`。算法求**最大模拟关系的不动点**；
失败时输出**可重放的攻击/应答策略图**（一般不是单条线性轨迹）；可选
**发散敏感**模式；搜索预算耗尽时明确回答**未决（undecided）**。

仅使用 Python 3.11 标准库，测试基于 `unittest`。

## 判定问题与方向约定

给定实现 LTS `I`（初态 `t0`）与规格 LTS `S`（初态 `s0`），判定是否存在
模拟关系 `R ⊆ Q_I × Q_S` 使得 `(t0, s0) ∈ R`。方向：**规格的每个动作都必须
能被实现匹配**；实现多出的动作不受约束（模拟是单向的）。关系中的状态对
一律记为 `(实现状态, 规格状态)`。

- 实现的内部 `tau` 动作**不需要**规格的任何动作来匹配；
- 死锁差异**不可忽略**：规格还能动作而实现已死锁（或无匹配动作）即为反例；
- 可见动作匹配走 **tau 闭包**（`tau* · a · tau*`），不是只比较邻接标签。

## 两种语义

**弱模拟**：`s -a-> s'` 由 `t =tau*=> · -a-> · =tau*=> t'` 匹配，要求
`(t', s') ∈ R`；`s -tau-> s'` 由 `t =tau*=> t'`（可为零步）匹配。

**分支模拟**：更强。可见动作 `a` 之前/之后的 tau 停顿段上，**途经的每个
状态都必须与当时有效的规格状态相关**（动作前与 `s` 相关，动作后与 `s'`
相关，执行 `a` 的那个实现状态也必须与 `s` 相关）。因此分支模拟关系 ⊆
弱模拟关系。示例 `examples/impl_stutter.json` vs
`examples/spec_choice.json`：弱模拟通过、分支模拟失败——实现先做 `a`
再内部 tau 停顿后才提供 `b`，但停顿后的状态已丢失规格仍要求的 `c` 分支。

## 算法：最大关系不动点

从全关系 `Q_I × Q_S` 出发逐轮删除"存在无法匹配的规格动作"的状态对，直到
不动点，得到**最大**模拟关系（greatest fixpoint）。每个被删对记录删除轮次
作为 **rank**。预算（`--max-rounds` / `--max-pair-checks`）耗尽且初态对尚未
被证伪时返回 **undecided**；已完成的轮次对初态对的删除是可靠的，此时仍
返回 fail。

## 反例：攻击/应答策略图（可重放）

失败时输出 JSON 见证（`--witness`）：

- **攻击节点**：规格的一步无法匹配的动作（`visible` / `stutter` /
  `divergence`）；
- **应答边**：实现的**所有**匹配应答及其落点状态对；沿应答边 rank 严格
  下降，故图有限且**终止**（叶子 = 实现无任何应答，含死锁；或发散攻击）；
- `trace` 字段：从根到叶投影出的**一条**线性动作轨迹（图本身一般无法压成
  单条轨迹）；
- `ranks` 与 `final_relation`：完整的不动点信息，供独立重放。

`replay` 子命令会**重新计算**不动点与整张策略图并逐边比对（含每个应答的
端点集合），篡改过的见证会被拒绝。

## 发散敏感模式（可选）

`--divergence-sensitive`：若实现某状态可进入无限 tau 计算（tau 环，用
tau 子图 SCC + 反向可达判定，迭代实现不会栈溢出）而对应规格状态不能，
则该对产生 `divergence` 攻击。普通模式下 tau 环被闭包 BFS 正常吸收，
不会无限展开。

## 独立全枚举验证

`enumerate` 子命令对小型模型**枚举全部 `2^|Q_I×Q_S|` 个关系**，取所有
后定点（`R ⊆ F(R)`）之并——由 Knaster–Tarski 定理即最大模拟。测试用它
与迭代不动点算法在示例模型和 60 个随机小模型（含高 tau 密度）上交叉验证
（两种语义 × 是否发散敏感），结果完全一致。枚举预算超限同样返回未决。

## 模型格式（JSON）

```json
{
  "name": "vending-impl",
  "initial": "q0",
  "transitions": {
    "q0": [["tau", "q1"]],
    "q1": [["coin", "q2"]],
    "q2": []
  }
}
```

## 使用

```bash
python3.11 -m simcheck check IMPL.json SPEC.json [--mode weak|branching] \
    [--divergence-sensitive] [--max-rounds N] [--max-pair-checks N] [--witness OUT.json]
python3.11 -m simcheck replay IMPL.json SPEC.json WITNESS.json
python3.11 -m simcheck enumerate IMPL.json SPEC.json [--mode ...] [--max-relations N]
```

退出码：`0` 通过 / `1` 失败（模拟不成立）/ `2` 未决 / `3` 用法或模型错误。

## 运行样例

```text
$ python3.11 -m simcheck check examples/impl_machine.json examples/spec_machine.json
status=pass            # 实现的内部 tau 被吸收（weak 与 branching 均通过）

$ python3.11 -m simcheck check examples/impl_deadlock.json examples/spec_progress.json
status=fail            # 死锁差异：规格要 a，实现无任何动作 -> 无应答叶子

$ python3.11 -m simcheck check examples/impl_divergent.json examples/spec_simple.json
status=pass            # 普通模式：tau 自环被弱模拟吸收
$ python3.11 -m simcheck check examples/impl_divergent.json examples/spec_simple.json --divergence-sensitive
status=fail            # 发散敏感：实现可发散而规格不能 -> divergence 攻击

$ python3.11 -m simcheck check examples/impl_stutter.json examples/spec_choice.json --mode weak
status=pass
$ python3.11 -m simcheck check examples/impl_stutter.json examples/spec_choice.json --mode branching \
    --witness examples/counterexample_branching.json
status=fail            # 分支模拟失败，见证已写出（4 轮收敛）
$ python3.11 -m simcheck replay examples/impl_stutter.json examples/spec_choice.json \
    examples/counterexample_branching.json
replay OK: witness is a valid counterexample for the initial pair

$ python3.11 -m simcheck check examples/impl_stutter.json examples/spec_choice.json \
    --mode branching --max-rounds 2
status=undecided       # 预算耗尽，未搜索完 -> 未决
```

`examples/counterexample_branching.json` 中的线性轨迹投影（完整策略图见
`strategy` 字段）：规格攻 `a`（实现 `a` 应答到 `i1`）→ 规格攻 `b`，实现
在保持与 `s1` 相关的前提下无法出 `b`（唯一的 `b` 要先经 tau 到 `i2`，而
`i2` 丢失了 `c` 分支）→ 终端原因 `no-response`。

## 测试

```bash
python3.11 -m unittest discover -s tests -v
```

结果：`Ran 44 tests ... OK`（约 30 秒，主要为全枚举交叉验证）。覆盖：
LTS 解析与校验、tau 闭包与 tau 环终止、5000 状态 tau 链的迭代 SCC、
弱/分支语义区分、死锁差异、发散敏感、预算未决、策略图 rank 严格下降、
见证重放与篡改拒绝、随机小模型全枚举交叉验证。

## 代码结构

- `simcheck/lts.py` — 有限 LTS 模型与 JSON 加载/校验
- `simcheck/relations.py` — tau 闭包、分支可达、发散 SCC、攻击/应答计算
- `simcheck/fixpoint.py` — 最大模拟不动点迭代、rank、预算
- `simcheck/strategy.py` — 攻击/应答策略图、线性轨迹投影、见证重放
- `simcheck/enumerate.py` — 独立全枚举验证器
- `simcheck/cli.py` — 命令行入口
- `examples/` — 示例模型与一份分支模拟反例见证
- `tests/` — unittest 测试套件

## 限制

- 验证对象仅限显式给出的有限状态模型；不做 on-the-fly 展开或符号化。
- 全枚举验证器复杂度为 `2^|Q_I×Q_S|`，仅适用于小模型（默认上限 2^20 个
  候选关系，超出返回未决）。
- 模拟是单向前向关系；未实现互模拟或迹等价等其他等价/预序。
