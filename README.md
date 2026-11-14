# 设备名被重新分配给了攻击者

工业网络按设备名分配地址，攻击者用名称分配请求抢走离线设备的名称，流量被引到自己。请实现供C++20工业发现服务调用的可导入名称所有权组件。名称绑定MAC、端口、站与租约，分配前核对现有所有权与冲突策略；离线设备的名称在保留期内不可抢占。改名、重置与多播请求受限。用名称抢占、冲突分配、保留期、合法分配和重置验证名称只归原设备。

## 实现

`netsec.hpp` 提供 `netsec::NameOwnership`（也可通过 `NameRegistry` 等别名
导入），无外部依赖。核心规则：

- 所有权键为名称（大小写不敏感、去除首尾空白），记录绑定到完整
  `DeviceIdentity`（MAC、端口、站）及租约。
- 只有完整身份一致的原设备才能续租、改名或重置。其他设备在租约有效时
  得到 `Conflict`，在离线保留期内得到 `ReservationActive`。
- 离线保留期从显式 `markOffline` 事件或租约到期开始；保留期结束前不会
  被抢占。原设备在保留期内重新出现可直接续租。
- 改名是原子的：目标名称的所有权、冲突和保留期检查全部通过后，旧名称
  才会被释放。
- 分配、续租、改名、重置和离线通知的 `multicast` 请求一律拒绝，避免
  广播/多播报文触发所有权变更。
- `ConflictPolicy::KeepOwner` 可配置为永不接管；默认
  `AllowAfterReservation` 仅在离线保留期结束后允许复用名称。

```cpp
#include "netsec.hpp"

netsec::NameOwnership registry;
const netsec::DeviceIdentity plc{"02:00:00:00:00:01", 34964, "line-1"};

auto assigned = registry.assign("plc-1", plc, std::chrono::seconds(60));
if (assigned) {
    // ...
}
```

离线/时钟可以显式注入，便于发现服务接入和确定性测试：

```cpp
registry.markOffline("plc-1", plc);
registry.advance(std::chrono::seconds(30));
```

`ctest --test-dir build` 运行名称抢占、冲突分配、保留期、合法分配、改名、
重置和多播限制的回归测试。
