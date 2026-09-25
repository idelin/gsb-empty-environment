# playout_sim — 包重排、播放时钟估计与自适应目标延迟

纯 Python 3.11 标准库实现，测试使用 `unittest`。不接音频设备：样本数组与
采样率由本地夹具提供（`playout_sim/fixtures.py`，48 kHz，确定性正弦叠加），
时间由离散事件模拟驱动（`playout_sim/events.py`）。

## 数据模型

每个包（`playout_sim/packets.py`）携带：

- `epoch`：流 epoch，流重启时递增；
- `seq`：epoch 内序号；
- `src_pos`：负载首个样本的源采样位置（`src_pos == seq * 480`）；
- `recv_time`：本地接收时钟，由网络模型在打点时写入；
- `samples`：负载样本（480 个 = 10 ms）。

## 声明的延迟与数值容差

延迟预算（`playout_sim/tolerances.py`）：

- 基础余量 `base_margin = 25 ms`；
- 自适应余量 `headroom ∈ [8 ms, 100 ms]`，快攻（逐 tick 取残差最大值）
  慢释（每 tick ×0.98，约 0.5 s 时间常数）；
- 目标延迟 = base_margin + headroom，即声明延迟区间 **33 ms … 125 ms**。

数值容差：

- 漂移估计误差 ≤ 30 ppm（`DRIFT_TOLERANCE_PPM`）；
- 稳态目标延迟超出基础余量 ≤ 12 ms（`TARGET_PRE_BURST_MARGIN_S`）；
- 突发后目标延迟回落容差 ≤ 2 ms（`DELAY_SETTLE_TOLERANCE_S`）；
- 漂移+突发场景下隐蔽（conceal）样本 ≤ 3000；
- 单调性为严格不等式（无容差）：`play_pos` 严格递增。

## 关键设计

- **时钟估计**（`clockest.py`）：对 `offset = recv_time - src_pos/fs`
  按 250 ms 分箱取低分位数（稳健地板），Theil-Sen 中位斜率拟合，再按
  残差剔除被突发污染的分箱做第二阶段拟合。抖动是非负尖峰，永不压低
  地板，因此突发不会被当作漂移；漂移经 `rate_ratio` 补偿，不会转化为
  永久延迟。
- **自适应延迟**（`controller.py`）：余量只跟踪拟合地板之上的抖动
  残差，突发结束后按指数衰减回落。
- **单调播放**（`receiver.py`）：`play_pos` 严格递增（每 tick 前进量
  夹在 `[0.25, 3.0]` 个块）。延迟收缩时**丢弃**真实样本
  （`dropped_samples`），延迟增长时**合成**样本（`grow_synth_samples`），
  缺数据时合成隐蔽样本（`conceal_missing_samples`）；合成样本在 trace
  与统计中均被标记。已播放区间拒绝迟到包改写（`late_*`），旧 epoch
  包拒绝（`old_epoch_*`），重复包丢弃（`duplicate_*`），缓冲满时保留
  最小序号集合、丢弃其余（`overflow_*`）——全部可追溯。
- **固定内存**：抖动缓冲上限 24 包（可配），估计器分箱窗口 6 s，
  控制器 O(1)。trace 仅为测试插桩（`trace_on`），不计入运行态内存。
- **快照/恢复**：`World.snapshot()` 产出 JSON 可序列化的完整状态
  （含事件队列、在途包、RNG 状态），`World.from_state()` 恢复后
  逐比特一致地继续。

## 守恒恒等式（测试断言）

```
sent + dup_injected == arrived + net_lost
arrived             == accepted + duplicate + overflow + late + old_epoch
accepted            == emitted_true + dropped + buffered_now
output              == emitted_true + emitted_synth == ticks * block
emitted_synth       == conceal_missing + grow_synth + preroll_synth
```

## 运行测试

```
python3.11 -m unittest discover -s tests -t . -v
```

实际运行结果见 `RESULTS.md`。
