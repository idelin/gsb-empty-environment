# TCP SACK记分板的碎片上限与最小MSS

攻击者把 TCP 的 MSS 协商得极小，再发送大量交错的 SACK 块，让发送端把重传队列切成数量庞大的小片段，遍历一次就耗尽 CPU，甚至拖垮整台主机。为C++17 TCP 栈写供 ACK 处理路径调用的 SACK 记分板，输入协商 MSS、发送队列的段（序号区间）和每个 ACK 带的 SACK 块列表：协商 MSS 低于调用方给的下限（默认 536）时按下限使用，且对端通告低于 48 时拒绝连接；SACK 块须落在 (snd_una, snd_nxt] 内，区间反向或越界的块忽略，同一 ACK 最多处理 4 个块；被 SACK 覆盖的相邻区间必须合并，记分板内区间数量不得超过 128，超出时放弃最旧的区间信息，回退到只重传 snd_una 起的一个段，不得无限增长；单个 ACK 的处理步数不超过区间数加块数的常数倍；重复 ACK 带同样的块不重复计数；重传按区间边界发送，不再切成 MSS 以下的碎片，除非是队列尾部。用 MSS 为 48、交错 SACK 洪泛、反向区间、同块重复 ACK 和合并后的区间计数验证。

## 实现

- `netsec.hpp` / `netsec.cpp`：`netsec::SackScoreboard`，供 ACK 处理路径调用。
  - `negotiate_mss(peer_mss)`：`peer_mss < 48` 返回 false（拒绝连接），否则有效 MSS 取 `max(peer_mss, min_mss)`，`min_mss` 默认 536。
  - `process_ack(snd_una, snd_nxt, blocks)`：校验并合并 SACK 块，返回新覆盖字节数；区间上限 128，超限淘汰最旧区间并进入回退态。
  - `retransmit_segments(snd_una, snd_nxt)`：按区间边界生成不超过 MSS 的重传段；回退态只返回 snd_una 起的一段。
- `test_netsec.cpp`：覆盖 MSS 48、交错 SACK 洪泛、反向/越界区间、重复 ACK、合并计数与重传切分。

## 构建与测试

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```
