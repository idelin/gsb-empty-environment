# ISO-TP 连续帧重组防护

面向 CAN 网关的可导入 ISO 15765-2（11 位标识、普通寻址、经典 CAN 8 字节帧）重组包，供 CAN 接收回调逐帧调用，抵御极长首帧、重复/乱序连续帧造成的缓冲耗尽与伪造载荷拼接。

## 组成

- `isotp.hpp` / `isotp.cpp`：无外部依赖的 C++17 库，可直接加入网关工程。
- `netsec::isotp::IsoTpChannel`：单个 CAN 标识的重组通道，接收回调逐帧调用。
- `netsec::isotp::IsoTpRouter`：按 11 位 CAN ID 自动建通道并隔离状态，拒绝大于 0x7FF 的标识。
- `tests/test_isotp.cpp`：158 项断言，覆盖 32 位逃逸长度取小值、SN 回绕、乱序/重复、孤立连续帧与 N_Cr 超时。

## 安全规则

- 单帧（SF，PCI 高半字节 0）：长度字段必须在 1–7 之间，且 DLC 不短于 `1 + 长度`，否则拒绝。
- 首帧（FF，PCI 高半字节 1）：取 12 位长度；该字段为 0 时按逃逸格式读取随后 4 字节的大端 32 位长度，且该值必须 `> 4095`，否则直接拒绝、不发流控。
- 缓冲上限：声明长度超过调用方配置上限（含缓冲为 0）时拒绝，并回送 `OVFLW`（FS=2）流控帧，不发生超量内存分配。
- 连续帧（CF，PCI 高半字节 2）：期望 SN 从 1 开始、模 16 递增（15 之后回到 0）；任何不匹配（乱序、跳号、重复）立即丢弃整个重组。
- 孤立 CF：没有进行中的重组时收到 CF 一律忽略，不建立任何状态。
- 重组进行中再次收到 FF：放弃旧重组，以新 FF 重新开始。
- N_Cr 超时：使用调用方提供的毫秒时钟，距上一帧时间严格超过 N_Cr 即丢弃；可由 `pollTimeouts()` 定时驱动，也会在下一次回调内自动判定，迟到的 CF 无法复活旧重组。

## 集成示例

```cpp
#include "isotp.hpp"

using namespace netsec::isotp;

// 每个标识的重组缓冲上限 4096 字节，N_Cr = 1000 ms。
IsoTpRouter router(/*bufferLimit=*/4096, /*nCrTimeoutMs=*/1000);

void onCanReceived(const CanFrame& frame, std::uint64_t nowMs) {
    RxResult r = router.onCanFrame(frame, nowMs);
    if (r.sendFlowControl) {
        canTransmit(r.flowControl);  // CTS（FS=0）或 OVFLW（FS=2）
    }
    if (r.completed()) {
        deliverUpstream(r.payload, r.length);  // 指针在下一次调用前有效
    }
}

// 定时器（如每 10 ms）调用，驱动 N_Cr 超时。
void onTimerTick(std::uint64_t nowMs) {
    router.pollTimeouts(nowMs);
}
```

`RxResult::status` 含义：`kComplete` 重组完成、`kFlowControl` 已接收 FF 需回 CTS、`kRejected` 非法帧或溢出、`kIgnored` 忽略的帧；`timedOut` 标记本次回调是否顺带清掉了一个超时重组。

## 构建与测试

```sh
cmake -S . -B cmake-build
cmake --build cmake-build
ctest --test-dir cmake-build --output-on-failure
```
