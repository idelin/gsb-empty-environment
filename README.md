# 整数时钟时间自动机可达性检查器

Python 3.11, 仅依赖标准库, 测试使用 unittest。

## 模型

`TimedAutomaton` 由以下要素构成有限状态模型:

- **整数时钟**: 非负整数值, 时间以整数步推进;
- **guard**: 迁移上的原子约束合取, 支持 `< <= == >= >`;
- **reset**: 迁移触发时将指定时钟置 0, 旧值不保留;
- **invariant**: 位置上的上界型约束合取, 限制驻留时间。

## 语义要点

- **显式时间上界**: `check_reachability(..., time_bound=T)` 限定累计时间不超过 T;
- **等价截断**: 令 K 为模型中最大约束常数, 时钟值超过 K 一律截断为 K+1。
  所有比较常数 <= K, 截断前后 guard 判定一致, 可达性保持等价且状态空间有限;
- **严格性**: `x<3` 与 `x<=3` 是不同约束, 边界点 x=3 上结论不同(见测试);
- **零时间环**: 不改变离散状态的自环被 visited 集合吸收, 不会无限扩展;
- **未决**: 扩展预算 `max_expansions` 耗尽且队列非空时返回 `UNKNOWN`,
  绝不把未搜索完误报为 `UNREACHABLE`;
- **可重放反例**: 可达结论附带 `Delay`/`Take` 步骤序列,
  `replay()` 可确定性重放并逐步校验, 任何篡改都会抛出 `ReplayError`。

## 运行

```sh
python3.11 example.py      # 运行样例, 输出时间推进轨迹
python3.11 -m unittest -v  # 运行测试
```

## 文件

- `timed_automaton.py` — 模型定义、BFS 可达性检查、轨迹重放与格式化
- `example.py` — 双时钟加热器样例
- `test_timed_automaton.py` — 20 个 unittest 用例
