# iosim：发送/接收状态机的模拟关系检查

把通信协议表示为发送/接收状态机，检查候选实现是否满足规范规定的
（交替）模拟关系；不兼容时返回可逐步重放、可独立执行的失败交互路径。
仅使用 Python 3.11 标准库，不依赖任何现成正则或自动机库。

## 模型

- 字母表与转移结构显式输入：`Machine(name, states, initial, inputs, outputs, transitions)`，
  也支持 `Machine.from_dict(...)` 从纯数据构造（见 `machine.py`）。
- 输入/输出分支方差明确：
  - 输入逆变 —— 候选机**多接收**规范未要求的输入，仍兼容；
  - 输出协变 —— 候选机**多发出**规范未允许的输出，不兼容。
- 循环协议：最大模拟关系用不动点迭代计算，有限状态上单调递减、必然终止
  （见 `simulation.py` 的 `greatest_simulation`）。

## 反例

- 初始状态对不在最大关系中时，在"坏对"图上 BFS 提取最短失败交互路径；
- 失败分两类：`missing_input`（候选缺少必需输入）、
  `unexpected_output`（候选发出额外输出）；
- `Counterexample.replay(candidate, spec, on_step=...)` 逐步重放并复核终点失败；
- `Counterexample.to_script(...)` 导出独立可执行的 Python 脚本（退出码 0 表示
  反例重放成功）。

## 运行

```sh
python3 sample_run.py    # 运行样例（含循环协议与三类候选）
python3 -m unittest -v   # 运行测试
```

## 样例输出（节选）

```
== candidate-ok vs spec-server ==
不动点迭代 2 轮后终止，最大模拟关系含 4 个状态对
结论: 兼容（候选机被规范模拟）

== candidate-bad-output vs spec-server ==
不动点迭代 4 轮后终止，最大模拟关系含 1 个状态对
结论: 不兼容
反例交互路径:
  1. (idle, idle) --req?--> (busy, busy)
  失败: 候选机在状态 busy 发出规范未允许的输出 error!
逐步重放:
  重放第 1 步: (idle, idle) --req?--> (busy, busy) 通过
  终点失败条件复核通过

== 独立反例脚本已导出: counterexample_replay.py ==
重放反例: 候选机 candidate-bad-output vs 规范 spec-server
  步骤 1: (idle, idle) --req?--> (busy, busy)
终点失败确认: 候选机在状态 busy 发出规范未允许的输出 error!
反例重放成功
独立脚本退出码: 0
```

## 测试结果

```
Ran 15 tests in 0.105s
OK
```

覆盖：输入/输出方差（增输入兼容、增输出不兼容、缺必需输入不兼容）、
循环协议不动点终止、非确定性规范分支覆盖、反例逐步重放与篡改拒绝、
独立脚本执行、机器定义校验。
