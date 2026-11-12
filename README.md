# PMP表项优先级与锁定

RISC-V 机器模式用 PMP 表项隔离 enclave 内存，攻击者想借表项优先级、TOR 与 NAPOT 的编码以及锁定位让自己的区域被放行。为C++17监视器实现供内存访问判定调用的 PMP 求值库：16 个表项按编号从小到大，首个覆盖访问全部字节的表项决定权限，只覆盖部分字节则访问失败；模式有 OFF、TOR、NAPOT、NA4，TOR 以前一表项地址为下界（首项下界为 0），NAPOT 按 pmpaddr 末尾连续 1 的个数决定区域大小；L 位置位时对 M 模式同样生效，未锁定表项只约束 S 与 U 模式；无匹配时 M 模式允许、S 与 U 模式拒绝。用部分重叠访问、TOR 首项、NAPOT 编码边界、锁定表项和无匹配验证。

## 使用

```cpp
#include "netsec.hpp"

netsec::PmpEvaluator pmp;
netsec::PmpEntry entry{};
entry.pmpaddr = 0x100;              // 原始 pmpaddr，不是字节地址
entry.mode = netsec::PmpMode::Napot;
entry.read = true;
entry.locked = true;
pmp.setEntry(0, entry);

netsec::PmpAccess access{};
access.address = 0x400;
access.size = 4;
access.privilege = netsec::PrivilegeMode::M;
access.type = netsec::AccessType::Read;
const auto decision = pmp.evaluate(access);
// decision == netsec::PmpDecision::Allow
```

也可以使用无状态的
`netsec::evaluatePmpAccess(entries, access)`，传入完整的 16 项数组。

`pmpaddr` 遵循 RISC-V 编码：字节地址为 `pmpaddr << 2`；NAPOT
中 `N` 为末尾连续 1 的个数，区域大小为 `2^(N+3)` 字节，区域起点
清除 pmpaddr 的低 `N+1` 位后左移两位。TOR 的上界不包含在区域内。

求值顺序如下：

1. OFF 表项不产生区间；M 模式跳过未锁定表项。
2. 从表项 0 开始查找与访问字节区间相交的最小表项。
3. 若该表项完整覆盖访问，则按访问类型及 R/W/X 位立即允许或拒绝。
4. 若只覆盖部分字节，立即拒绝，不再尝试后续表项。
5. 没有相交表项时，M 模式允许，S/U 模式拒绝。

零长度访问及跨越 64 位地址空间末尾的访问会被拒绝。

## 构建与测试

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```
