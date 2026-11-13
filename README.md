# ISO-TP 连续帧重组防护

CAN 网关用 ISO 15765-2 重组多帧消息时，攻击者可发送声称极长长度的首帧、
重复或乱序的连续帧，耗尽缓冲或拼出伪造载荷。本目录提供一个可直接导入的
C++17 重组包（经典 CAN、11 位标识、普通寻址），供 CAN 接收回调逐帧调用。

## 文件

- `netsec.hpp` / `netsec.cpp`：`netsec::IsoTpReassembler` 重组状态机，无外部依赖。
- `test_isotp.cpp`：覆盖攻击面与边界条件的自检（断言式，无需测试框架）。

## 防护规则

- 首帧（PCI 高半字节为 1）：普通格式取 12 位长度；长度字段为 0 时，字节 2..5
  为 32 位逃逸长度，且该值必须 `> 4095`，否则按协议错误拒绝（防止用逃逸格式
  伪装小长度）。
- 长度超过调用方配置的 `max_payload`：丢弃重组并排队一帧溢岀流控
  （FC 状态 2，CAN id 回显发送方），由网关回调发出。
- 连续帧序号从 1 开始，15 之后回到 0；序号与期望值不符（重复、乱序、陈旧）
  立即丢弃整个重组。
- 未建立会话时收到的连续帧一律忽略；不同 CAN id 的连续帧不影响当前会话。
- 重组进行中又收到合法首帧：放弃旧重组，以新首帧重新开始。
- 单帧长度字段必须在 1..7 之间且不超出 DLC，否则拒绝。
- 调用方通过 `now` 提供时钟；相邻帧间隔严格大于 `n_cr_ticks` 即判定 N_Cr 超时，
  丢弃部分重组；也可用 `poll()` 在周期性任务里回收超时会话。

## 集成示例

```cpp
#include "netsec.hpp"

netsec::ReassemblerConfig cfg;
cfg.max_payload = 4096;   // 网关缓冲上限
cfg.n_cr_ticks  = 1000;   // N_Cr，单位与调用方时钟一致
netsec::IsoTpReassembler rx(cfg);

void on_can_receive(const netsec::CanFrame& frame, uint64_t now) {
    netsec::RxResult r = rx.feed(frame, now);

    netsec::FlowControl fc;
    while (rx.take_flow_control(fc)) {
        can_transmit(fc.to_can_frame());   // 发出 CTS 或溢岀 FC
    }

    if (r.event == netsec::RxEvent::Complete) {
        deliver_to_upper_layer(r.sender_id, r.payload, r.length);
    }
}
```

`RxResult::payload` 指向重组器内部缓冲，仅在本次返回有效，下一次
`feed()`/`poll()` 后失效，上层需立即拷贝。

## 构建与测试

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```

不依赖 CMake 时也可直接编译：

```sh
clang++ -std=c++17 -Wall -Wextra -I. netsec.cpp test_isotp.cpp -o test_isotp
./test_isotp
```

测试覆盖：32 位逃逸长度取小值（0、1、7、4094、4095 拒绝，4096 接受）、
逃逸溢出回送溢出流控、SN 15→0 回绕（两轮）、乱序与重复序号中止、
孤立连续帧忽略、首帧重启会话、N_Cr 超时边界（含 `poll()`）、
完整 4096 字节逃逸消息载荷校验，以及外来 CAN id / 非法帧过滤。
