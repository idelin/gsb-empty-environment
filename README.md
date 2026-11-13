# SecOC 新鲜度值低位重建（netsec）

AUTOSAR SecOC 在报文里只带新鲜度值的低位，接收方要自己补全高位；攻击者利用回绕与不同步让旧报文被接受，或把接收方计数器推到未来而拒绝合法报文。本包为 C++17 ECU 提供可导入的新鲜度重建组件，供 PDU 接收路径直接调用。

## 算法约定

- 本地维护 64 位单调计数器 `L`，报文仅携带低 8 位。
- 候选完整值 `V` 取严格大于 `L`、且低 8 位等于报文值的最小整数，增量范围恒为 `[1, 256]`。
- `V - L` 超过调用方配置的前向窗口（如 128）即拒绝，防止计数器被推向未来。
- 重建（`reconstruct`）是纯查询，不修改任何状态；只有 MAC 验证通过后调用 `commit(V)` 才推进 `L`。
- MAC 验证失败调用 `notifyMacFailure()`，`L` 不变；低位回绕到 0 产生的高位进位只随成功的 `commit` 生效。
- 连续 3 次 MAC 失败（阈值可配）进入重同步状态：普通报文在重建阶段即被拒绝，只有受信同步消息经 `applyTrustedSync()` 装载完整计数器后才能恢复。
- 计数器接近 `UINT64_MAX` 时返回 `CounterOverflow`，绝不静默回绕归零。

## 导入方式

```cmake
add_subdirectory(path/to/netsec)
target_link_libraries(your_ecu_app PRIVATE netsec)
```

## PDU 接收路径集成

```cpp
#include "netsec.hpp"

netsec::FreshnessValidator freshness(/*initial_counter=*/0,
                                     /*forward_window=*/128);

// 普通 SecOC PDU：重建与 MAC 校验由本组件编排，MAC 算法由调用方注入。
bool accepted = freshness.processNormalPdu(pdu.freshness_low,
    [&](std::uint64_t reconstructed_freshness) {
        return verify_secoc_mac(pdu, reconstructed_freshness);
    });

// 也可以拆开调用，便于与既有 MAC 流水线对接：
auto rebuilt = freshness.reconstruct(pdu.freshness_low);
if (!rebuilt) {
    return reject(rebuilt.reason);       // ResyncActive / OutOfWindow / ...
}
if (verify_secoc_mac(pdu, *rebuilt.candidate)) {
    freshness.commit(*rebuilt.candidate);
} else {
    freshness.notifyMacFailure();
}

// 受信同步消息（其真实性由独立的受信通道保证）：
freshness.applyTrustedSync(sync_message.full_counter);
```

`FreshnessValidator` 不含内部锁，每个实例应只在单一 PDU 接收上下文中使用。

## 构建与测试

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```

测试覆盖：256 回绕边界与进位时机、前向窗口边界（含 `window == 0`）、窗口外跳变、MAC 失败不推进 `L`、重放旧值与过期 `commit` 拒绝、连续失败进入/退出重同步、重同步期间普通报文与 MAC 均不触发，以及 64 位溢出。
