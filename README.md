# playdelay — 包重排、播放时钟估计与自适应目标延迟

纯标准库实现（Python 3.11），测试使用 `unittest`。样本数组与采样率由本地
夹具（`playdelay/fixtures.py`）提供，不接触任何音频设备。数据源为离散事件
模拟（`playdelay/sim.py`）。

## 运行

```sh
python3.11 -m unittest discover -v
```

## 流模型

每个包携带（`playdelay/model.py: Packet`）：

- `epoch`：流 epoch；旧 epoch 的包一律拒绝（`rejected_old_epoch` 计数）。
- `seq`：序号，用于重排与重复检测（固定大小位图窗口）。
- `src_pos`：载荷首个样本的源采样位置。
- `send_time`：源时钟发送时间；接收时间 `recv_time` 由传输层随包递交。

## 组成

- **包重排**（`buffer.py`）：固定容量抖动缓冲（默认 16 包），重复 / 迟到 /
  溢出分别分类计数，全部可追溯（`model.py: Stats`）。
- **播放时钟估计**（`clock.py`）：对 (接收时间, 发送时间) 做折扣最小二乘
  （固定内存），斜率估计源时钟漂移；播放速率 = 名义采样率 × 斜率，慢漂移
  由**速率**吸收，而不是变成永久加延迟。
- **自适应目标延迟**（`controller.py`）：对**漂移校正后**的传输时延做
  EWMA 抖动估计，余量 = clamp(4 × 抖动, 40ms, 200ms)，突发结束后自动衰减，
  不会被记忆成永久延迟。
- **播放引擎**（`player.py`）：目标延迟调整保持播放位置单调——
  - 扩张：插入**合成样本**（零阶保持插值），在 `output_flags` 中标记并计入
    `grow_synth_samples`；
  - 收缩：整包向前跳过（`dropped_shrink_samples`），播放位置只前进不后退，
    绝不重复播放；
  - 缺包：到期未至的区间以同样插值隐藏（标记合成、计入 missing），之后到达
    的包判为迟到并丢弃——已播放区间永不改写。

## 声明延迟与数值容差

见 `playdelay/config.py`：

- 目标延迟 = 60ms 基础 + 自适应余量 [40ms, 200ms]，即标称 100–260ms；
  样本级目标另受 `(容量-4) 包` 上限约束（为丢弃滞回与速率限制留出余量）。
- `TIME_TOL = 1e-9`（时间比较）；`RATE_SNAP_TOL = 2e-3`（斜率吸附到 1.0 的
  容差）；`SLOPE_TEST_TOL = 5e-4`（测试对漂移估计的容差）。

## 固定内存

引擎状态为 O(1)：缓冲 ≤ 容量个包、重复检测为 4096 位位图、估计器均为
EWMA/折扣回归。`output` / `trajectory` / `monitor` 等是验证用记录（测试
脚手架），随运行时长增长，不属于引擎状态。

## 测试（tests/）

- `test_clock_drift_vs_jitter.py`：5000ppm 慢漂移 + 突发抖动同时出现，
  二者不会被混为永久加延迟（斜率跟踪漂移、余量随突发升落后回落、
  漂移校正后的抖动估计不被污染、突发前零丢弃）。
- `test_epoch_and_monotonic.py`：旧 epoch 包拒绝；迟到包不能改写已播放
  区间；延迟收缩不重复播放；重复 / 溢出 / 缺包均可追溯。
- `test_determinism_and_conservation.py`：保存/恢复（可恢复两次）与随机
  投递顺序产生完全相同的调度轨迹；报告真实与补偿样本守恒（四条恒等式）。

实际运行结果见 `RESULTS.md`。
