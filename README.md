# WPS PIN 分半验证与持久化锁定

这是一个面向 C++17 路由器固件的 WPS PIN 验证库。它把校验位检查、前 4 位与后 3 位的合并验证，以及跨重启的失败锁定放在同一个状态机入口中，避免调用方在第一半失败时提前返回。

## 接入

```cpp
#include "netsec.hpp"

netsec::WpsPinVerifier verifier(
    "12345670",                         // 设备配置中的 AP PIN
    "/var/lib/wps/pin-lock.state");     // 持久化状态

const netsec::RegistrarMac mac{0x02, 0, 0, 0, 0, 1};
const netsec::VerificationResult result = verifier.verify("22345677", mac);

if (result.succeeded()) {
    // 继续 WPS 交换
} else if (result.locked()) {
    // 统一返回锁定错误；result.retry_after 为剩余秒数
} else {
    // Failure、InvalidPin、InvalidChecksum 均不包含哪一半失败的细节
}
```

状态机也可以提交已经拆开的材料：

```cpp
netsec::PinCommitment commitment;
commitment.first = {2, 2, 3, 4};
commitment.second = {5, 6, 7};
commitment.checksum = 7;
const auto result = verifier.verify(commitment, mac);
```

## 行为

- 校验位按前 7 位的奇数位之和乘 3、偶数位之和相加，再对 10 取补。
- 校验位错误返回 `InvalidChecksum`，非法数字或长度返回 `InvalidPin`，两者都在进入分半协议前拒绝且不增加失败计数。
- 第一半和第二半使用固定次数的比较；即使第一半不匹配，也会完成第二半比较。最终只根据两半合并后的差异返回 `Failure`，不暴露出错位置。
- 连续失败达到策略阈值（默认 3 次）后返回 `Locked`。锁定期间在解析或比较 PIN 前直接返回，不执行校验和计算或分半比较。
- 锁定时间从 60 秒开始逐次翻倍，最多 86400 秒（24 小时）。默认策略下依次为 60、120、240、…、61440、86400、86400 秒。
- 失败计数、锁定层级和锁定截止时间写入状态文件。文件通过同目录临时文件、`fsync` 和原子 `rename` 更新，进程重启或设备重启后重新读取。
- 注册方 MAC 只是状态机的上下文，不参与锁定键；更换 MAC 不会清除锁定。
- 一次成功验证清零失败计数和锁定层级，下一次锁定重新从 60 秒开始。

## 测试

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```

测试覆盖：

- 已知 PIN 校验位、非法数字和错误校验位。
- 第一半错误与第二半错误的相同失败码和相同响应字段。
- 3 次失败锁定、重启后仍锁定、更换 MAC 后仍锁定。
- 60 秒到 24 小时的逐次翻倍与封顶。
- 成功后失败计数和退避层级清零。
- 枚举全部 10000 个前 4 位值，并比较其与第二半错误枚举的平均响应时间。
