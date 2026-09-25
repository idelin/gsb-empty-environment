# audiosched — 多轨音频与控制事件统一调度器

纯 Python 3.11 标准库实现，不接触任何音频设备。样本数组与采样率由本地
夹具提供（`audiosched/fixtures.py`），渲染结果以普通数据（`OutputBlock`）
返回，并用逐样本参考实现直接核验。

## 声明

- 采样率：48 000 Hz；块大小：128 样本
- **声明延迟**：恰好一个渲染块 = 128 / 48000 ≈ 2.667 ms
- **数值容差**：1e-9（与逐样本参考渲染器比对时的最大绝对误差）

## 语义

- **统一调度**：增益、路由、滤波（单极点低通）控制事件与音频共享同一
  样本时钟；块内在事件点精确切段，每段用当时生效的参数渲染。
- **事务**：同一样本位置的事件作为一个事务提交。先在状态副本上校验并
  应用全部事件，再对最终路由图做环检测；任一参数非法则整体回滚并记入
  审计日志。因此同一样本先后两个路由事件不会让块短暂进入非法环——
  块只会看到事务提交前或提交后的状态，绝不看到中间态。
- **epoch / version**：事件携带样本时钟 epoch 与版本号。切换 epoch 后
  旧 epoch 事件被丢弃且绝不误触发；同一 `event_id` 只保留最高 version。
- **迟到更正**：目标输出块尚未确认（未播放）时，恢复块起始 checkpoint
  并重算未确认尾部；已确认（已播放）则生成审计冲突（audit conflict），
  已播放样本绝不被改写。
- **checkpoint**：`checkpoint(name)` / `restore(name)` 保存并恢复参数、
  路由、滤波器状态、样本位置、epoch 与已应用事件集合；每个块起始也
  自动保存 checkpoint 供迟到更正重算。
- **暂停/恢复**：`pause()` 可发生在事件应用之后、输出确认之前；暂停
  期间 `render_block()` 返回 `None`；`resume()` 后按输出 ID 去重——
  已确认的输出绝不重发，未确认的输出按 ID 原地替换。
- **块边界**：恰好落在块边界的事件只在以该样本起始的块中触发一次。

## 运行

```sh
python3.11 -m unittest discover -s tests -v   # 测试
python3.11 run_replay.py                       # 重放全部样例
```

## 结构

- `audiosched/events.py` — 控制事件定义（epoch / version / kind / params）
- `audiosched/graph.py` — 有向路由图、环检测、拓扑排序
- `audiosched/scheduler.py` — 块调度器：切段、事务、迟到更正、checkpoint、暂停/恢复
- `audiosched/reference.py` — 独立的逐样本朴素参考渲染器（核验用）
- `audiosched/fixtures.py` — 样本数组、采样率、声明延迟与容差
- `audiosched/replay.py` — 可重放样例场景（确定性，可反复重放）
- `tests/test_scheduler.py` — unittest 测试套件
- `run_replay.py` — 重放入口
