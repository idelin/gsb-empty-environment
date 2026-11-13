# UDS安全访问尝试与会话

UDS 的 SecurityAccess（0x27）靠 seed 与 key 解锁诊断功能，攻击者靠重放 key、暴力尝试，或在会话切换后沿用已解锁状态绕过。为C++ ECU 写可导入的安全访问状态机，供诊断服务分发器调用：奇数子功能请求 seed，偶数子功能（前一个奇数加 1）发送 key；已解锁的级别再请求 seed 时返回全零 seed；每次请求 seed 由调用方的随机源生成新值，key 校验失败后该 seed 作废并返回 NRC 0x35；连续 3 次 key 错误返回 NRC 0x36 并锁定 10 秒，锁定期内的 seed 请求返回 NRC 0x37；key 请求前没有对应 seed 请求返回 NRC 0x24；会话切换（0x10）到另一会话或会话超时后所有级别回到锁定；子功能不是已登记级别返回 NRC 0x12。用 key 重放、两次错误后正确、锁定期内请求、顺序错误和会话切换验证。

## 接口

`netsec.hpp` 提供 `netsec::SecurityAccessStateMachine`（别名
`netsec::SecurityAccess`）。seed 随机源、key 校验器和单调时钟均由调用方注入：

```cpp
netsec::SecurityAccess security(
    [] { return generateFourByteSeed(); },
    [] { return std::chrono::steady_clock::now(); });

security.registerLevel(0x01,
    [](const netsec::Seed& seed, const netsec::Key& key) {
        return calculateKey(seed) == key;
    });

auto result = security.handle(subfunction, key);
```

`handle` 对奇数子功能返回 seed；对偶数子功能校验 key。正响应在
`result.positive` 中表示，seed 位于 `result.seed`；负响应通过
`result.nrc` 返回 0x12、0x24、0x35、0x36 或 0x37。分发器处理 0x10
会话切换时调用 `onSessionChange(session)`，会话超时时调用
`onSessionTimeout()`。失败计数和解锁状态机内部维护；会话切换只撤销所有
已解锁级别，不会清除当前锁定计时或连续失败计数。
