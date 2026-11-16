# EtherCAT 邮箱帧校验包

头文件-only 的 C++17 校验器，供主站邮箱读取回调用，防御被控制从站在长度字段、邮箱计数器、FoE 文件名和 CoE 段 toggle 位上的篡改。一个从站邮箱通道对应一个 `MailboxValidator` 实例（非线程安全）。

## 校验规则

邮箱头按小端解析，共 6 字节：长度 2、地址 2、通道/优先级 1、类型 4 位 + 计数器 3 位 + 保留 1 位。

- 帧不足 6 字节，或同步管理器容量小于 6：拒绝（`TooShort` / `SmTooSmall`）。
- 头长度 `L` 必须同时满足 `L <= SM 大小 - 6` 与 `L <= 实际收到字节 - 6`，否则分别返回 `LengthExceedsSm`、`LengthExceedsReceived`。
- 计数器为 0（保留值）拒绝（`CounterZero`）；与上一帧相同视为重传，返回 `RetransmitDiscard`，由调用方丢弃；跳号只经日志回调记录，仍然放行；7 -> 1 回绕视为正常。
- 只放行 `enableType()` 登记的类型，未登记返回 `TypeNotRegistered`；类型 3 为 CoE，4 为 FoE。
- FoE：载荷不足 4 字节拒绝；仅写请求（opcode 2）检查偏移 4 起的文件名，文件名必须以单个 NUL 结尾，且不得含 `/`、`\`、连续两个点（`..` 段）或任何内嵌字节 0，空名也拒绝。
- CoE：载荷不足 2 字节拒绝；SDO 命令字节（CoE 头之后第一个字节）的 cs 字段为段传输（cs=0）时，首个段 toggle 必须为 0，之后每次必须与上一段交替，违反返回 `CoeToggleError` 且不推进期望值；SDO initiate（cs=1）把下一段期望复位为 0，abort（cs=3）清除段状态。

## 回调集成示例

```cpp
#include "netsec.hpp"

using netsec::mailbox::MailboxValidator;
using netsec::mailbox::kTypeCoe;
using netsec::mailbox::kTypeFoe;

void onMailboxRead(MailboxValidator& validator,
                   const uint8_t* frame,
                   std::size_t receivedBytes) {
    const auto result = validator.validate(frame, receivedBytes);
    if (result.disposition ==
        netsec::mailbox::FrameDisposition::RetransmitDiscard) {
        return; // 重传，直接丢弃
    }
    if (!result.accepted()) {
        return; // 非法帧，result.reason 给出原因
    }
    // result.payload / result.payload_length 指向 frame + 6 的可信载荷
}

// 初始化时按从站邮箱 SM 容量构造，并登记允许的邮箱类型
MailboxValidator makeSlaveValidator(std::size_t syncManagerSize) {
    MailboxValidator validator(syncManagerSize,
        [](const std::string& msg) { logSlaveEvent(msg); });
    validator.enableType(kTypeCoe);
    validator.enableType(kTypeFoe);
    return validator;
}
```

`ValidationResult::payload` 指向调用方传入的缓冲，仅在该缓冲存活期间有效；校验器不拷贝数据，也不持有缓冲。

## 构建与测试

```sh
cmake -S . -B build-make -G "Unix Makefiles" \
      -DCMAKE_CXX_COMPILER=clang++
cmake --build build-make
ctest --test-dir build-make --output-on-failure
```

或直接编译单测：

```sh
clang++ -std=c++17 -Wall -Wextra -Wpedantic -Werror -I. \
        test_mailbox_validator.cpp -o test_mailbox_validator
```

测试覆盖：长度超过 SM 容量、长度超过实发字节、计数器 0、重传丢弃、跳号记录不拒绝（含 7->1 回绕）、未登记类型、FoE 路径穿越与分隔符/内嵌 NUL/未终止文件名、CoE toggle 错误与首个段 toggle 约束。
