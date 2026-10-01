# POR Verifier — 基于读写集合的偏序归约模型检查器

纯 Python 3.11 标准库实现的有限状态模型验证器。通过动作的读/写集合识别
独立动作，构造 ample（持久）集合以削减等价交错，同时保持死锁与指定的
安全性质（state-predicate safety）。测试使用 `unittest`。

## 归约条件（Clarke/Grumberg/Peled 第 10 章的 ample-set 构造）

- **独立性**：动作 a、b 相关（dependent）当且仅当一方写入了另一方读或写
  的变量。**写同一变量的两个动作永不独立**（write-write 冲突自动相关）。
- **C0**：ample 集非空当且仅当 enabled 集非空。
- **C1（持久性）**：ample 集之外的动作在与 ample 动作相关时不可能先执行。
  保守实现：从种子动作出发做依赖闭包，吸收所有 enabled 的相关动作；若某
  相关动作当前 disabled（可能经其他动作变为 enabled），则拒绝该种子。
- **C2（环保护 / cycle proviso）**：归约图中的每个环必须包含一个完全扩展
  的状态。在线实现：未完全扩展的状态若有后继在 DFS 栈上，则改为完全扩展。
  这防止被忽略的动作沿环被无限期推迟（ignoring problem）。
- **C3（可见性）**：ample 集是 enabled 的真子集时，其中动作不得写安全性质
  所读的变量（`property_vars`）。

满足 C0–C3 时，归约图保持全部死锁与安全性质的真值。

## 判定与反例

- `safe`：状态空间搜索完毕，无违反、无死锁。
- `unsafe`：发现性质违反或死锁，附带**可重放**的动作名轨迹
  （`explore.replay` 可从初始状态逐步重放并到达违例状态）。
- `pending`：因 `max_states` / `max_depth` 截断而未搜索完，保持未决。

每个被扩展的状态都会记录归约选择证据（enabled、chosen、是否完全扩展、
种子接受/拒绝理由、环保护触发等），见 `explore.format_evidence`。

## 运行

```sh
python3.11 main.py                  # 演示：全枚举 vs POR、证据与反例重放
python3.11 -m unittest discover -v  # 测试
```

## 运行样例（`python3.11 main.py` 摘要）

```
== independent_counters(n=2,k=3) ==
   full: verdict=safe    states=16   transitions=24
   por : verdict=safe    states=7    transitions=6

== shared_counter(n=2,k=4) ==        # 共享变量 → 无归约
   full: verdict=safe    states=9    transitions=16
   por : verdict=safe    states=9    transitions=16

== flag_race ==                      # 环保护找回被忽略的动作
   full: verdict=unsafe  counterexample=property_violation trace=['toggle', 'set_y']
   por : verdict=unsafe   counterexample=property_violation trace=['toggle', 'set_y']
   replay: trace reaches {x=1, y=1}; counterexample confirmed

== deadlock_model ==                 # 死锁在归约下保持
   full: verdict=unsafe  counterexample=deadlock trace=['inc', 'inc', 'setx']
   por : verdict=unsafe   counterexample=deadlock trace=['inc', 'inc', 'setx']

== flag_race without cycle proviso (ignoring problem) ==
   naive POR: verdict=safe  <-- 错误：set_y 沿 toggle 环被永久忽略
```

`flag_race` 的归约证据（节选）：

```
#0 state={x=0, y=0}
   enabled=['toggle', 'set_y'] chosen=['toggle'] fully_expanded=False
   - seed 'toggle': persistent set ['toggle'] selected; ignored ['set_y'] are independent ...
#1 state={x=1, y=0}
   enabled=['toggle', 'set_y'] chosen=['toggle', 'set_y'] fully_expanded=True
   - cycle proviso: successor via ['toggle'] is on the DFS stack; expanding all enabled ...
```

## 测试结果（`python3.11 -m unittest discover`）

```
Ran 14 tests in 0.057s
OK
```

覆盖：独立性判定（写-写冲突必相关）、独立/共享/可见性三类模型的归约
效果、死锁保持、环保护修复 ignoring problem、截断保持 `pending`、反例
轨迹可重放，以及 300 个随机小模型上 POR 与全枚举的判定一致性对照
（verdict 相同、POR 状态数不超过全枚举、反例均可重放验证）。

## 文件结构

- `por/model.py` — 状态、动作（含读/写集合）、模型与安全性质定义
- `por/explore.py` — 独立性判定、ample 集选择、带环保护的 DFS、证据与重放
- `por/examples.py` — 示例模型（独立计数器、共享计数器、flag_race、死锁等）
- `main.py` — 演示入口
- `tests/test_por.py` — unittest 测试（含随机小模型对照）
