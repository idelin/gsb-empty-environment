# J1939 传输协议与地址声明

J1939 的传输协议用 BAM 广播或连接模式传多包数据，攻击者伪造 TP.CM 控制帧、重复声明地址或制造超时，让接收端占满缓冲或收下拼错的数据。本仓库提供一个可导入 C++ ECU 的 J1939 BAM 重组器，供 CAN 接收回调调用。

## 接入

```cpp
#include "netsec.hpp"

netsec::TpReassembler tp;

void on_can_frame(std::uint32_t id,
                  const std::uint8_t* data,
                  std::uint8_t dlc,
                  std::uint64_t now_ms) {
    auto result = tp.on_frame(id, data, dlc, now_ms);
    if (result.completed) {
        consume(result.pgn, result.source_address, result.payload);
    }
}
```

`now_ms` 使用调用方单调时钟，单位为毫秒。也可以先填充 `netsec::CanFrame`，再调用 `on_frame(frame)`。没有后续 CAN 帧时，可周期性调用 `expire_sessions(now_ms)` 清理超过 750 ms 的会话。地址声明既可通过 PGN 60928 帧自动处理，也可调用 `on_address_claim(source_address, name)`。

## 安全规则

- `TP.CM_BAM` 控制字节必须为 32；总字节数必须在 9 到 1785 之间，总包数必须等于 `ceil(total_bytes / 7)`，否则拒绝。
- 每个源地址只保留一个 BAM 会话；新的合法 BAM 会替换旧会话。
- `TP.DT` 序号必须从 1 连续递增，乱序、重复、非法序号或帧长不足都会中止会话。
- 相邻包间隔超过 750 ms 时中止会话，恰好 750 ms 仍然有效。
- PGN 60928 地址声明按 NAME 数值仲裁，较小 NAME 保留地址；冲突导致胜出者变更时清除该地址的重组会话。
- 源地址 254 或 255 的 TP 帧直接忽略。

## 构建与测试

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```

测试覆盖总包数不符、乱序/重复、两个 BAM、超时边界和地址声明冲突。
