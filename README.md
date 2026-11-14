# 楼宇总线组地址授权组件

`netsec` 是一个可由 C++20 总线网关直接链接的组地址授权库。它不只检查报文
中的源地址，而是要求网关把**传输层认证后的发送者身份**单独传入。组地址按
`区域 / 设备类型 / 功能` 三级登记，只有同时满足成员关系、区域与设备类型、
功能授权、广播/集中组显式授权、防重放与速率限制的命令才会被放行。

## 安全模型

- **源地址伪造**：`IncomingCommand::claimed_source` 来自不可信总线报文，
  `authorize()` 的 `authenticated_source` 必须来自网关认证层。两者不一致立即
  拒绝，且不会用伪造身份查询组权限。
- **跨区/跨类型**：普通区域组要求发送者与组地址的区域和设备类型完全一致。
- **成员关系与功能权限**：发送者必须显式加入目标组，且该组的成员策略必须
  包含命令功能码。命令功能码还必须与组地址中的功能字段一致。
- **广播与集中组**：普通 `add_member()` 不足以放行，必须调用
  `grant_broadcast()` / `grant_central()` 做显式授权。
- **重复与重放**：非零序号在 `replay_window` 内不能重复；相同发送者、组、
  功能和载荷在 `duplicate_window` 内也不会重复下发。
- **异常速率**：每个“发送者 + 组”使用独立令牌桶，超过突发量即拒绝。
- **只达授权组**：`authorize_many()` 返回的 `authorized_targets` 只包含通过
  授权的目标组；调用方不得向 `results` 中未放行的组发送命令。

所有注册与授权方法均线程安全（内部互斥锁）。

## 快速使用

```cpp
#include "netsec.hpp"

netsec::GroupAuthorizer gateway;

// 设备身份由网关的配置/认证层维护。
gateway.register_device("1.1.1", 1, 1);      // 区域 1，照明
gateway.register_device("0.7.1", 0, 7);      // 中央控制器

// 组地址：区域 1 / 照明 / 开灯功能。
const netsec::GroupAddress zone1_on{1, 1, netsec::function::light_on};
gateway.register_group(zone1_on, netsec::GroupKind::zone);
gateway.add_member(zone1_on, "1.1.1", {netsec::function::light_on});

netsec::IncomingCommand frame{
    .claimed_source = "1.1.1",
    .target = zone1_on,
    .function = netsec::function::light_on,
    .sequence = 42,
    .payload = "on",
};

auto decision = gateway.authorize(frame, "1.1.1");
if (decision.allowed()) {
    // 只向 decision.target 路由。
}
```

广播/集中组示例：

```cpp
const netsec::GroupAddress broadcast{0, 0, 0x20};
gateway.register_group(broadcast, netsec::GroupKind::broadcast);
gateway.add_member(broadcast, "0.7.1", {0x20});          // 仍会拒绝
gateway.grant_broadcast(broadcast, "0.7.1", {0x20});     // 显式放行
```

`authorize_many()` 可用于一条报文请求多个目标组时做过滤：

```cpp
std::array<netsec::GroupAddress, 3> requested{zone1_on, zone2_on, broadcast};
auto routed = gateway.authorize_many(frame, "1.1.1", requested);
// routed.authorized_targets 只含确实授权的组。
```

## 构建与测试

```sh
cmake -S . -B build -DCMAKE_CXX_STANDARD=20
cmake --build build
ctest --test-dir build --output-on-failure
```

测试覆盖合法控制、组地址/源地址伪造、广播与集中组滥用、跨区命令、功能越权、
重复序号、相同载荷重放、令牌桶速率限制，以及多目标路由过滤。
