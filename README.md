# IO 协议状态机模拟检查

这个小项目把通信协议表示为显式发送/接收状态机，检查候选协议是否满足规范协议的 IO 交替模拟关系。只使用 Python 标准库和 `unittest`，不使用正则、自动机或图算法第三方库。

## 文件

- `protocol_sim.py`：字母表、动作、转移、状态机、最大不动点检查和失败路径重放。
- `demo.py`：一个请求/数据协议和四个候选，可直接运行。
- `test_protocol_sim.py`：8 个 `unittest` 测试。

## 模拟关系

动作标签中的 `?` 表示输入/接收，`!` 表示输出/发送。

- 候选必须接受规范状态启用的每个输入。
- 候选可以增加规范没有启用的输入动作；这是更宽的可接收能力。
- 对规范的每个输入后继，必须存在仍保持关系的候选后继。
- 候选的每个输出动作和输出后继都必须能被规范对应状态允许。
- 候选可以省略规范中的可选输出，但不能新增规范未允许的输出。
- 循环不做步数上限假设；从全部状态对开始迭代，删除不满足局部条件的状态对，直到达到最大不动点。

检查失败时返回 `FailurePath`，其中包含：

- 初始规范/候选状态对。
- 每一步比较的输入或输出分支。
- 终端阻塞类型：`missing required input` 或 `extra output`。
- 独立的输入轨迹和输出轨迹。

`replay_failure` 会重新从初始状态启用每条转移并验证终端阻塞；`iter_replay_failure` 是逐步生成器，可逐帧重放。被篡改的路径会抛出 `ReplayError`。

## 运行

```sh
python3.11 -m unittest -v
python3.11 demo.py
```

## 本次验证结果

测试环境：`Python 3.11.15`。

```text
test_bad_cycle_is_removed_by_fixed_point_and_replayed ... ok
test_compatible_when_candidate_adds_input_and_omits_optional_output ... ok
test_extra_output_is_incompatible ... ok
test_invalid_state_machine_is_rejected ... ok
test_loop_reaches_greatest_fixed_point_for_valid_cycle ... ok
test_missing_required_input_returns_terminal_failure_path ... ok
test_replay_rejects_tampered_path ... ok
test_shared_successor_can_cover_multiple_required_input_branches ... ok

----------------------------------------------------------------------
Ran 8 tests

OK
```

`python3.11 demo.py` 输出 1 个通过候选和 3 个失败候选。典型失败路径如下：

```text
[FAIL] extra-output (fixed-point iterations: 3)
alternating simulation failed from (spec=ready, candidate=ready)
(spec=ready, candidate=ready) required input branch: spec ready --open?--> open; candidate ready --open?--> open
(spec=open, candidate=open) candidate output: spec <none>; candidate open --reset!--> ready
terminal: extra output reset! at (spec=open, candidate=open)
input trace: open?
output trace: reset!
replay #1 required input branch (spec=ready, candidate=ready) spec ready --open?--> open; candidate ready --open?--> open
replay #2 candidate output (spec=open, candidate=open) spec <none>; candidate open --reset!--> ready
replayed 2 step(s) independently
```
