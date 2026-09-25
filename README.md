# audiosched — 多轨音频与控制事件统一调度器(离线)

Python 3.11,仅标准库。样本数组与采样率由本地夹具提供
(`audiosched/fixtures.py`),不接触任何音频设备。

## 声明

- 采样率: `SAMPLE_RATE = 48000`,块大小: `BLOCK_SIZE = 64`
- 声明延迟: `DECLARED_LATENCY_SAMPLES = 64`(一个渲染块的处理延迟;无设备延迟)
- 数值容差: `NUMERIC_TOLERANCE = 1e-9`(与逐样本参考实现比对时使用)

## 语义

- 控制事件(增益 / 路由 / 滤波)携带样本时钟 `epoch` 与单调 `version`。
- 块内按事件点精确切段;同一位置的若干事件作为**一个事务**提交:
  任一参数非法或提交后路由成环,则整体回滚,块永远不会经过非法中间态
  (同一样本先断后连的路由交换因此安全)。
- 迟到更正(更高 version)只允许重算**尚未确认**的输出;已确认(已播放)
  部分产生审计冲突(`audit` 中 `type == "conflict"`),状态保持不变。
- `checkpoint()` / `restore()` 提供命名检查点;引擎另在每个块边界自动
  快照,供迟到更正回滚。路由环检测见 `audiosched/graph.py`。
- `set_epoch()` 切换时钟纪元后,旧纪元事件被隔离(quarantine),不会误触发;
  块边界事件恰好触发一次。
- `pause()` 在事件应用之后、输出确认之前生效;`resume()` 后按输出 ID
  `(epoch, start)` 去重,已确认块不会重复发出。

## 布局

- `audiosched/events.py` — 事件类型与构造器
- `audiosched/graph.py` — 有向路由图 + 确定性环检测(Kahn 拓扑排序)
- `audiosched/dsp.py` — 一阶低通滤波器(跨块保持状态)
- `audiosched/engine.py` — 调度引擎(切段、事务、迟到更正、检查点、暂停/恢复)
- `audiosched/fixtures.py` — 样本夹具、采样率、延迟与容差声明
- `audiosched/scenarios.py` — 5 个可重放样例场景
- `replay.py` — 重放入口: `python3.11 replay.py [场景名 ...]`
- `tests/test_engine.py` — unittest 套件(含复杂交错的逐样本参考核验)

## 运行

```sh
python3.11 replay.py                 # 重放全部场景,打印审计轨迹与摘要
python3.11 -m unittest discover -s tests -v
```
