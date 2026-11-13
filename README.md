# TCP SACK 记分板的碎片上限与最小 MSS

`netsec` 提供一个供 TCP ACK 处理路径调用的 C++17 SACK 记分板。它把对端通告的 MSS 限制到调用方下限，按序号窗口过滤 SACK 块，维护有界且合并后的已 SACK 区间，并生成不会退化成大量微小片段的重传规划。

## 使用方式

```cpp
#include "netsec.hpp"

netsec::SackScoreboard scoreboard(48);
scoreboard.reset({
    netsec::SeqRange{0, 10000},
});

auto result = scoreboard.process_ack(
    0,
    {
        netsec::SeqRange{1000, 2000},
        netsec::SeqRange{3000, 4000},
    });

for (const auto& range : scoreboard.retransmission_plan()) {
    use_range(range.begin, range.end);
}
```

上面的 `scoreboard(48)` 使用默认下限 `536`，因此实际有效 MSS 为 `536`。`use_range()` 代表调用方按区间执行重传。

## 行为约定

- `negotiate_mss()` / `SackScoreboard::create()` 在通告 MSS 小于 `48` 时返回空；构造函数此时抛出 `std::invalid_argument`。有效 MSS 为 `max(通告 MSS, 调用方下限)`，下限默认 `536`。
- 记分板窗口为 `(snd_una, snd_nxt]`：块必须满足 `begin > snd_una`、`end <= snd_nxt`，且 `begin < end`。反向、零长度或越界的整块被忽略。
- 每个 ACK 只检查前 4 个块。重复块只计入 `blocks_duplicate`，不会改变区间或 `newly_sacked_bytes`。
- 重叠或相邻的已 SACK 区间会合并。区间总数始终不超过 `128`；超限时从最低序号一端淘汰最旧的区间。
- 发生超限时进入回退状态，`retransmission_plan()` 只返回从 `snd_una` 开始的一个不超过有效 MSS 的段；累计 ACK 推进 `snd_una` 后解除回退。
- 重传规划以 SACK 区间边界为界，不按最小 MSS 以下切碎。除队列尾部外，每个重传区间长度不小于有效 MSS；为避免微小空洞，少量会跨越已 SACK 字节，但不会产生无界区间数。
- `process_ack()` 的记分板更新步数为 `O(区间数 + 块数)`，通过 `last_processing_steps()` 可观测；块数上限为 4。

## 构建与测试

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```

测试覆盖 MSS 48 下限、交错 SACK 洪泛、反向/越界块、4 块上限、重复 ACK、相邻区间合并和重传片段尺寸。
