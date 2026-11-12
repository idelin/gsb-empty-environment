# VF 邮箱与源地址约束

SR-IOV 的 VF 由租户客户机控制，攻击者可通过邮箱请求改写 MAC、加入未授权 VLAN，或在发送描述符里伪造源 MAC 和叠加 VLAN 标签。为C++17 PF 驱动写供邮箱与发送路径调用的 VF 约束库：管理员设定了 MAC 时 VF 不得更改，VLAN 只能加入允许表内的 ID，端口 VLAN 生效时带内标签的帧丢弃，发送源 MAC 必须等于分配值或在允许列表内，混杂模式只对 trusted 的 VF 开放。用改 MAC、越权 VLAN、双层标签、伪造源地址和 trusted 切换验证。

## 接口

- `netsec::VfGuard` 保存单个 VF 的管理员策略、邮箱请求状态和发送约束。
- `netsec::VfGuardManager` 按 VF ID 保存多个隔离的 `VfGuard`，供 PF 邮箱与传统发送路径查询。
- 管理员接口 `admin_*` 是可信路径，可更新分配 MAC、VLAN 允许表、源 MAC 允许表、端口 VLAN 和 trusted 状态。
- 客户机接口 `request_*` / `handle_mailbox()` 只能通过邮箱路径调用，不能绕过管理员策略。
- `check_tx_frame()` 在发送前检查帧长、VLAN 标签栈和源 MAC，返回允许或明确的丢弃原因。

```cpp
#include "netsec.hpp"

netsec::VfGuard vf(netsec::MacAddress{0x02, 0x10, 0x20, 0x30, 0x40, 0x50});
vf.admin_set_allowed_vlans({100, 200});
vf.admin_allow_source_mac(netsec::MacAddress{0x02, 0xAA, 0xBB, 0xCC, 0xDD, 0xEE});
vf.admin_set_port_vlan_enabled(true);

// 邮箱路径
auto status = vf.handle_mailbox(
    netsec::MailboxRequest::set_mac(
        netsec::MacAddress{0x02, 0x13, 0x37, 0x00, 0x00, 0x01}));
// status == netsec::Status::PermissionDenied

// 发送路径
auto result = vf.check_tx_frame(frame_data, frame_size);
if (!result) {
    // result.reason: MalformedFrame / PortVlanTagPresent /
    //                SourceMacNotAllowed
}
```

## 策略语义

- 管理员分配 MAC 后，VF 的 `SetMac` 邮箱请求返回 `PermissionDenied`；管理员可显式清除分配后重新授权。
- 管理员 VLAN 允许表之外的 `JoinVlan` 返回 `PermissionDenied`；允许表收紧时，VF 已加入但不再被允许的 VLAN 会被撤销。
- 端口 VLAN 开启时，外层 EtherType 为 `0x8100`、`0x88A8` 或常见 Q-in-Q TPID 的帧直接丢弃，包括单层和双层标签。
- VLAN 标签栈最多解析 8 层，截断或过深的标签栈按 `MalformedFrame` 丢弃。
- 源 MAC 仅接受管理员分配值或源 MAC 允许表中的值；未配置分配 MAC 且允许表为空时默认拒绝。
- 非 trusted VF 请求混杂模式返回 `NotTrusted`；管理员撤销 trusted 时，已开启的混杂模式立即关闭。

## 构建与测试

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```

测试覆盖管理员 MAC 锁定、越权 VLAN、单/双层标签、伪造源 MAC、trusted 切换、邮箱分发和多 VF 隔离。
