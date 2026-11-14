# 物联网入网密钥组件

面向 C++20 网络协调器的可导入信任中心组件。它修复的核心问题是：旧安装码
不能再被用来加入网络并获得当前网络密钥。

## 安全模型

- **设备白名单**：只有 `register_device()` 登记过的身份才能申请安装码或入网；
  未知设备一律拒绝。
- **安装码绑定身份且一次性**：安装码由根密钥、设备身份、随机 nonce 和当前密钥
  代次共同认证。换到其他设备身份会返回 `InstallCodeDeviceMismatch`；成功入网、
  为同一设备重新签发安装码、或提交密钥轮换后，旧码失效。
- **重复与克隆入网拒绝**：已经入网的设备再次入网返回 `DeviceAlreadyJoined`；
  克隆身份无法复用原设备安装码。已导出设备私钥的物理克隆需要平台硬件根信任，
  仅靠软件无法区分，但本组件不会向不存在/未登记的身份下发密钥。
- **代次化网络密钥**：每个代次使用独立随机 32 字节网络密钥。
  未提交轮换期间禁止新设备入网，避免代次歧义。
- **逐设备加密下发**：轮换时每个已入网设备得到独立 nonce、密文和认证标签。
  `KeyDelivery` 中不含明文网络密钥；只有持有该设备入网凭据的一方才能通过
  `open_key_update()` 解出候选密钥，且设备身份和代次都在认证范围内。
- **确认后提交**：`begin_key_rotation()` 只产生待确认代次；所有在线设备
  `confirm_key_update()` 成功后，协调器才提交新代次。错误凭据、重复确认和
  旧代次确认都不会推进状态。
- **回退保护**：已提交代次单调递增；`rollback_generation()` 始终拒绝，旧代次
  确认返回 `GenerationRollbackBlocked`，不会恢复历史网络密钥。

拒绝路径的 `JoinResult` 不包含 `network_key`，`device_secret` 也为空。轮换结果
只包含逐设备密文，不包含明文网络密钥。

## 接口示例

```cpp
#include "netsec.hpp"

netsec::TrustCenter tc;

// 信任中心登记设备并签发一次性安装码（通过带外安全信道交付）。
tc.register_device("sensor-17");
auto issue = tc.issue_install_code("sensor-17");

// 设备使用自身身份和安装码入网。
auto joined = tc.join("sensor-17", issue.code);
if (!joined.accepted()) {
    return; // 旧码、错误身份、未知设备、重复入网等都不会返回网络密钥。
}

// 两阶段轮换：逐设备下发密文，确认齐备后提交。
auto rotation = tc.begin_key_rotation();
for (const auto& delivery : rotation.deliveries) {
    // delivery 通过该设备的受保护控制通道发送。
    // 设备侧：auto key = netsec::open_key_update(delivery, device_secret);
    // 协调器侧收到确认后：
    // tc.confirm_key_update(delivery.device_id, delivery.generation,
    //                       device_secret);
}
```

安装码和网络密钥均为接口中的字节/字符串对象。协调器应通过安全信道交付安装码、
逐设备轮换密文和设备入网凭据，并持久化已登记设备及当前代次，防止重启后重放。

## 构建与验证

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Debug
cmake --build build
ctest --test-dir build --output-on-failure
```

测试覆盖旧码/替换码入网、未知设备、身份错绑、重复入网、设备克隆尝试、合法入网、
逐设备密文隔离、错误确认凭据、轮换期间入网阻断、密钥代次回退，以及轮换后网络
密钥不泄漏。
