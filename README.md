BBRv1 在浅缓冲队列下容易引发持续高丢包，BBRv2 引入了基于显式丢包率与 ECN 标记率的双界模型（`bw_lo`/`bw_hi` 与 `inflight_lo`/`inflight_hi`）以及细分的 `ProbeBW` 四子相状态机，四处核心机制一旦耦合失准就会导致带宽坍塌或公平性失衡：1）**四态主状态机与 `ProbeBW` 四子相循环**：实现 `STARTUP`（增益 `2.89`，除传统连续 3 轮带宽增长 `< 25%` 退出外，一旦单轮内出现过多丢包次数或高丢包/ECN 率即提前退出并立刻锁定当前 `inflight_hi`！）-> `DRAIN`（增益 `1/2.89` 排空队列至 `BDP`）-> `PROBE_BW`（细分为 `DOWN -> CRUISE -> REFILL -> UP` 四子相！）-> `PROBE_RTT`（若 `min_rtt` 超过 `10s` 未刷新，将 `cwnd` 压至 `min(0.5 * BDP, max(4 MSS, ...))` 并维持至少 `max(200ms, 1 RTT)` 以测量真实传播延迟！）；2）**`inflight_hi` / `inflight_lo` 与 `bw_lo` 短期/长期双界更新**：发生丢包或 ECN 比例超过阈值（如 `loss_thresh = 2%` 或 `ecn_thresh = 50%`）时，在 `PROBE_UP` 阶段将 `inflight_hi` 收缩至已安全交付的 `inflight_latest` 附近（留出 `beta=0.85` 退让或 `headroom=15%` 空间！），而在任意阶段同时更新短期下界 `inflight_lo` 与 `bw_lo`（按 `beta` 衰减！）限制当前发包速率；进入新一轮探测周期重置短期下界但保留长期上界 `inflight_hi`；3）**`ProbeBW` 四子相精准转换**：`DOWN`（0.9x 增益）排空过剩队列直至 `inflight <= BDP` 或经过 1 RTT 转入 `CRUISE`（1.0x 稳态，持续随机/确定性探测间隔且受 `inflight_hi * (1 - headroom)` 封顶！）-> `REFILL`（维持 1 RTT 将管道充至 `inflight_hi`）-> `UP`（1.25x 增益，每轮次按 `1, 2, 4, ...` 指数增加探测包数尝试突破 `inflight_hi`，若触发丢包/ECN 或超上限立刻退回 `DOWN`！）；4）**带宽窗口过滤与精确交付率（Delivery Rate）采样**：正确区分 `app_limited` 样本（应用受限时绝不拉低最大带宽估计 `bw_hi`！）并支持带瓶颈链路+浅缓冲+RED/ECN 标记的确定性离散事件网络仿真器对比收敛行为。为传输控制层交付可导入的 Go BBRv2 拥塞控制状态机与确定性瓶颈仿真引擎。验收覆盖 `STARTUP` 遇高丢包提前退出与 `inflight_hi` 锁定、`ProbeBW` 四子相顺序转换与 `headroom` 封顶、`ECN` 标记触发 `inflight_lo`/`bw_lo` 退让、`app_limited` 不污染 `bw_hi` 以及 `PROBE_RTT` 定时触发与恢复。

---

## 实现总览

本仓库交付两个可直接 import 的 Go 包，全部时间由外部（虚拟）时钟驱动，状态机本身不读墙钟，因此完全确定性、可重放。

- `bbr` — BBRv2 拥塞控制状态机（纯逻辑，无网络依赖，可嵌入真实传输层或仿真器）。
- `netsim` — 确定性离散事件瓶颈仿真引擎（单瓶颈 FIFO 浅缓冲 + 尾丢 + 阈值 RED/ECN 标记 + 多流）。
- `example` — 单流端到端运行示例：`go run ./example`。

## 包结构

- `bbr/config.go`：状态/子相枚举与全部可调参数（增益、`beta`、`headroom`、丢包/ECN 阈值、PROBE_RTT 定时等）及 `DefaultConfig()`。
- `bbr/bbr.go`：核心 `BBR` 结构、`OnSent`/`HandleAck`/`Tick` 驱动、轮次（round）记账、显式丢包/ECN 拥塞判定与双界适配。
- `bbr/control.go`：四态主状态机与 ProbeBW 四子相转换、`inflight_hi/lo` 与 `bw_lo` 更新、`cwnd`/pacing 计算、PROBE_RTT 进入与恢复。
- `bbr/sampler.go`：交付率采样与 `bw_hi` 窗口最大过滤器，`app_limited` 样本绝不参与更新。
- `netsim/netsim.go`：虚拟时钟、事件堆、瓶颈队列、尾丢与确定性阈值 ECN 标记、统计。
- `netsim/flow.go`：发送端 pacing 令牌桶、应用 offer-rate 令牌桶（app-limited 标记）、累积 ACK/3 重复 ACK/RTO 丢包检测与重传、到 `bbr.AckEvent` 的桥接。

## 四项核心机制的落点

1. **四态主状态机 + ProbeBW 四子相**
   - `STARTUP`：pacing/cwnd 增益 `2.89`，窗口按轮次倍增并以 `2.89×BDP` 封顶；连续 3 轮带宽增长 `<25%` 退出，或单轮丢包率 `>=2%`（且达到绝对丢包下限）/STARTUP 期 ECN 标记即提前退出，退出瞬间按 `inflight_latest × (1-headroom)` 锁定长期上界 `inflight_hi`。
   - `DRAIN`：增益 `1/2.89`，排空至 `inflight <= BDP` 后进入 `PROBE_BW/DOWN`。
   - `PROBE_BW`：严格 `DOWN(0.9x) -> CRUISE(1.0x) -> REFILL(1.0x, 1 RTT) -> UP(1.25x)`，UP 每轮探测量 `1,2,4,...` 指数增长，触发显式信号或超上限即回 `DOWN`。
   - `PROBE_RTT`：`min_rtt` 满 `10s` 未刷新即进入，`cwnd = min(0.5×BDP, max(4 MSS, …))`，维持至少 `max(200ms, 1 RTT)` 且排空到该窗口后恢复到新一轮 `DOWN`，并刷新 `min_rtt` 时间戳。
2. **双界模型**
   - 仅在 `STARTUP` 与 `ProbeUP` 对显式信号反应（DOWN/CRUISE/REFILL 已处于安全包络内，避免短期界被持续衰减而坍塌）。
   - 触发时 `bw_lo`、`inflight_lo` 按 `beta=0.85` 衰减；UP 阶段同时把 `inflight_hi` 收缩到 `inflight_latest` 附近。
   - 进入下一轮 UP 探测（REFILL→UP）时重置短期下界 `inflight_lo/bw_lo`，长期 `inflight_hi` 跨周期保留。
3. **四子相精确转换**：DOWN 在 `inflight<=BDP` 或满 1 RTT 后转 CRUISE；CRUISE 受 `inflight_hi×(1-headroom)` 封顶并按确定性 `CruiseDuration` 计；REFILL 充管 1 RTT；UP 指数探测，显式信号立即退回 DOWN。
4. **交付率采样与 app_limited**：窗口最大过滤器只接受非 app-limited 样本更新 `bw_hi`；整窗均为 app-limited 时保留历史全应用样本，绝不让空闲应用拉低估计。

## 验收测试映射

`go test ./...`（可加 `-race`）覆盖：

- `TestStartupEarlyExitLocksInflightHi` / `TestStartupECNEarlyExit`（`bbr`）：STARTUP 遇高丢包/ECN 提前退出并锁定 `inflight_hi`。
- `TestProbeBWSubphaseOrderAndHeadroom`（`bbr`）：四子相 `DOWN→CRUISE→REFILL→UP` 顺序与 CRUISE 的 `headroom` 封顶。
- `TestECNBacksOffLowerBounds`（`bbr`）：UP 期 ECN 触发 `inflight_lo/bw_lo` 的 `beta` 退让、`inflight_hi` 收缩并回退 DOWN。
- `TestAppLimitedDoesNotPolluteBwHi`（`bbr`）：app-limited 低速率样本不污染 `bw_hi`，真实全应用样本仍可抬高估计。
- `TestProbeRTTTimingAndRecovery`（`bbr`）：10s 定时进入、半 BDP/4 包下限窗口、`max(200ms,1RTT)` 后恢复并刷新时间戳。
- `netsim` 端到端：`TestDeterminism`（同配置两次运行逐计数器一致）、`TestEndToEndStateProgression`（四态四子相）、`TestECNEnabledLinkMarksPackets`（ECN 链路标记与 UP→DOWN 退让）、`TestAppLimitedFlowDoesNotCollapseBwHi`、`TestTwoFlowDeterministicSharing`（多流共享瓶颈）。

## 仿真模型说明

- 时间为纳秒整数，事件按 `(时间, 入队序号)` 全序排列，保证无随机、可重放。
- 瓶颈服务时间 = `MSS / RateBps`；队列满尾丢；队列长度达到 `ECNThreshold` 后所有到达包打 CE（确定性阈值 RED-lite）。
- 接收端支持乱序缓存与累积 ACK；发送端以 3 重复 ACK 快速重传、RTO 兜底，重传在 BBR 在途记账上严格闭合（丢失 -1、重传 +1、最终 ACK -1）。
- `AddFlow(id, appRateBps, cfg)` 中 `appRateBps<=0` 表示应用永远 backlogged；正值则用令牌桶限速并把对应突发标记为 app-limited。
