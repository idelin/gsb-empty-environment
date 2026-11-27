# CET Shadow Stack / IBT State Validator

面向 C++17 系统安全宿主的可嵌入验证库。它不执行真实 x86 指令，而是对
Intel CET 的关键安全状态做符号化建模，供协程调度器、信号/中断返回路径和
内核-用户切换代码在提交状态前做决定。

库只依赖 C++17 标准库，无第三方依赖；在
`-Wall -Wextra -Wconversion -Werror` 下构建。

## 建模范围

- 48 位规范形式的 64 位虚拟地址空间，区分用户地址与内核地址。
- CPL 0 / CPL 3。
- 每线程 `SSP`、影子栈可用区间、已压入返回帧数。
- 4 KiB 页属性：普通可写页、只读影子栈页。
- 指令流状态 `TrackerState::Idle` / `TrackerState::WaitForEndbranch`。
- 正常的 CALL/RET 双栈校验、`INCSSPQ` 步长和帧链边界。
- `RSTORSSP`、`SAVEPREVSSP`、信号/中断返回令牌验证。
- 内核到用户的降权返回；用户态借令牌提权会被拒绝。
- 间接 CALL/JMP 的 `ENDBR64` 状态机，包括异常/信号边界保存与恢复。

该库是状态验证器，不修改硬件 CET 状态，也不实现真实页表或信号帧拷贝。

## 构建与测试

```sh
cmake -S . -B build
cmake --build build --parallel
ctest --test-dir build --output-on-failure
```

测试覆盖：

- CALL 与 RET 返回地址不一致、影子栈空栈和越界。
- `INCSSPQ` 超过 255、超过当前帧链、跨页或空洞。
- 合法令牌切换、Busy Bit 二次复用、双线程同令牌竞争仅一个成功。
- 同页但非精确自指、错误模式位、保留位、错误 CPL、普通可写页令牌。
- 用户 `SSP` 指向内核影子栈、令牌后一页为普通可写页。
- 缺少 `ENDBR64`、条件/无条件直跳绕过、异常边界恢复待校验状态。
- 内核到用户信号返回成功，用户借令牌提权失败。

## 恢复令牌布局

令牌固定为 8 字节，地址要求 8 字节对齐。为了执行“精确自指”校验，本库的
符号令牌把完整令牌地址放在 bit 63:3，因为低 3 位必为零：

| 位 | 含义 |
|---|---|
| 0 | Busy Bit |
| 1 | 模式位：1 = CPL 3，0 = CPL 0 |
| 2 | 保留位，必须为 0 |
| 63:3 | 令牌自身的 8 字节对齐虚拟地址 |

这是验证模型的符号编码，不是可直接写入真实 CET 硬件内存的 ABI。真实硬件
令牌的地址字段按 4 KiB 页编码；这里刻意采用更严格的 8 字节精确自指比较，
从而拒绝同一页内指向另一个槽位的伪造令牌。

`RSTORSSP` 的检查顺序是：地址对齐、CPL 地址范围、影子栈页属性、保留位、
模式位、精确自指、Busy Bit。全部通过后，在同一互斥临界区内完成
“检查 Busy Bit + 置位”，因此两个线程不能同时消费同一个恢复令牌。

## 快速使用

```cpp
#include "cet/cet.hpp"

#include <cstdint>

int main() {
    using namespace cet;

    constexpr Address stack = 0x0000000010000000ULL;
    constexpr Address code_a = 0x0000000000401000ULL;
    constexpr Address code_b = 0x0000000000402000ULL;

    CetMachine machine;
    if (!machine.map_page(stack, PageKind::ShadowStackReadOnly).ok()) {
        return 1;
    }

    ThreadState thread;
    if (!machine.attach_thread(
            thread, Cpl::Ring3, stack, stack + kPageSize).ok()) {
        return 1;
    }

    if (!machine.direct_call(thread, code_a, code_b).ok()) {
        return 1;
    }
    if (!machine.ret(thread, code_a).ok()) {
        return 1;
    }

    // 间接分支执行后，下一条取指必须是 ENDBR64。
    if (!machine.indirect_jump(thread, code_b).ok()) {
        return 1;
    }
    if (!machine.fetch(thread, Opcode::Endbr64).ok()) {
        return 1;
    }

    return 0;
}
```

每个入口返回 `Result`；`Result::code` 为具体 `Status`，失败状态不会提交
SSP、Busy Bit 或 Tracker 状态变更。调用方应把任何非 `Status::Ok` 视为
控制流完整性故障并终止当前转换。

## 关键不变量

- CALL 将返回地址压入影子栈；RET 仅在调用者给出的正常栈返回地址与影子栈
  顶槽完全一致时弹出。
- `INCSSPQ` 同时受 255 槽架构上限和当前已压入帧数约束，不能跳过尚未
  返回的上层帧。
- 任何影子栈地址必须属于当前目标 CPL 的规范地址区间，并且落在只读影子
  栈页；普通可写页不能承载 `SSP` 或恢复令牌。
- 用户态不能通过令牌或 `switch_shadow_stack` 提升到 CPL 0。
- 异常/信号进入时保存 Tracker，处理期间使用独立 IDLE 状态；返回后恢复
  `WaitForEndbranch`，因此原间接分支目标仍必须从 `ENDBR64` 开始。
