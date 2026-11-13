# EtherCAT邮箱帧长度与文件名

EtherCAT 主站收到从站邮箱数据时，攻击者控制的从站可在 FoE 文件名、CoE 段传输或长度字段上做手脚。本仓库提供可导入的 C++17 邮箱帧校验包（`netsec` 库），供邮箱读取回调调用。

## 校验规则

- 邮箱头 6 字节：长度 2、地址 2、通道与优先级 1、类型 4 位 + 计数器 3 位 + 保留 1 位。
- 头里的长度不得大于调用方给的邮箱同步管理器大小减 6，也不得大于实际收到字节数减 6。
- 计数器取 1 到 7 循环，0 保留；与上一帧计数器相同视为重传丢弃，跳号只记录不拒绝。
- 类型 3 为 CoE，4 为 FoE，只放行调用方登记的类型。
- FoE 写请求（opcode 2）的文件名不得含 `/`、`\`、`..` 段或字节 0。
- CoE 段传输的后续段 toggle 位必须交替（初始化段后首段 toggle 为 0）。

计数器与 CoE toggle 状态只在帧被放行时推进，被拒绝的帧不污染会话状态。

## 用法

```cpp
#include "netsec.hpp"

netsec::MailboxValidator validator(/*sm_size=*/512);  // 每个从站会话一个实例
validator.allowType(netsec::kMailboxTypeCoE);
validator.allowType(netsec::kMailboxTypeFoE);

// 邮箱读取回调中:
netsec::CheckResult r = validator.validate(frame.data(), frame.size());
switch (r.verdict) {
  case netsec::Verdict::kAccept:        /* 处理邮箱数据 */ break;
  case netsec::Verdict::kDropDuplicate: /* 重传帧,静默丢弃 */ break;
  case netsec::Verdict::kReject:        /* 记录 netsec::toString(r.reason) */ break;
}
```

## 构建与测试

```sh
cmake -S . -B build && cmake --build build
ctest --test-dir build --output-on-failure
```

测试（`tests/test_mailbox.cpp`）覆盖：长度字段过大（相对 SM 与实际接收）、计数器 0、重传丢弃、跳号记录、未登记类型、FoE 路径穿越（`../`、`/`、`\`、`..`、内嵌 NUL、空名）、CoE toggle 未交替/未初始化段/正确交替序列。
