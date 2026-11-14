# VTEP 准入组件

面向 C++20 虚拟网络网关的可导入 VXLAN VTEP 准入库。组件在解封装后、写入
VNI 转发表前执行准入，拒绝伪造外层源、跨租户 VNI、内层源 MAC 冲突、分片、
超大帧和放大请求，并按原因累计计数。

## 集成

```cpp
#include "netsec.hpp"

netsec::AdmissionConfig config;
config.initial_tunnel_epoch = {7, 0x11223344};
config.sizes.max_inner_frame_bytes = 1500;
config.sizes.max_outer_datagram_bytes = 1550;

netsec::VtepAdmission admission(config);
admission.register_vtep(
    *netsec::Ipv4Address::parse("192.0.2.10"), 1001, 42);

netsec::EncapsulatedFrame frame;
frame.outer_source = *netsec::Ipv4Address::parse("192.0.2.10");
frame.vni = 1001;
frame.generation = 7;
frame.tunnel_key = 0x11223344;
frame.inner_frame = decapsulated_ethernet_frame;  // 原始以太网帧

const auto result = admission.admit(frame);
if (!result.accepted()) {
    // 丢弃；不要在拒绝后继续学习 MAC 或转发。
}
```

`outer_source`、分片标志和帧长必须来自网关已经确认的外层 IPv4/UDP 元数据，
不能取自 VXLAN 载荷。组件不解析外层 IP，避免调用方把攻击者控制的载荷误当作
地址凭据。

## 准入顺序

1. 检查 VNI 范围、IP 分片和内外层帧长。
2. 外层源地址必须已登记，且该 VTEP 获准进入报文 VNI。
3. VNI 只能属于一个租户；包内隧道代次/密钥必须匹配该租户当前代次，或处于
   一次配置轮换宽限期的上一代次。
4. 广播、组播和未知单播的复制份数不能超过 VNI 配置上限。
5. 每个外层源执行包数/字节令牌桶限速。
6. 内层源 MAC 按 VNI 学习；若该 MAC 已绑定其他 VTEP，则判定冲突。

只有全部检查通过才学习 MAC。未知 VTEP 不会污染真实 VTEP 的 MAC 绑定。

## 隧道轮换

```cpp
// 显式保留上一代次一个配置宽限窗口（默认立即退役旧代次）。
admission.rotate_tunnel_epoch(42, {8, 0x55667788}, true);

// 下一次轮换退役旧代次。
admission.rotate_tunnel_epoch(42, {9, 0xAABBCCDD}, false);

// 也可对全部已登记租户同步轮换。
admission.rotate_global_tunnel_epoch({10, 0x01020304}, true);
```

`TunnelEpoch` 同时比较 `generation` 和 `key`。生产部署应在创建
`VtepAdmission` 时通过 `initial_tunnel_epoch` 注入配置密钥；默认值 `{0, 0}`
仅用于未配置密钥的开发环境。进程重启时如仍处于轮换宽限期，可用
`initial_previous_tunnel_epoch` 同时注入上一代次。

## 计数

```cpp
const netsec::AdmissionCounters stats = admission.counters();
// stats.admitted / stats.dropped
// stats.unknown_vtep / stats.vni_mismatch / stats.inner_mac_conflict
// stats.fragmented / stats.oversized / stats.amplification_limited
// stats.tunnel_epoch_mismatch / stats.malformed_inner_frame
```

`admit` 是线程安全的，可在多个收包线程间共享一个实例。`admit(frame, now)` 的
显式时间戳便于确定性地测试令牌桶。

## 测试

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Debug
cmake --build build --parallel
ctest --test-dir build --output-on-failure
```

测试覆盖伪造外层源、VNI/租户错配、内层 MAC 冒用、分片/超大帧、广播/未知单播
放大、令牌桶限速、密钥代次轮换、注销回收，以及合法 VTEP 的接纳。
