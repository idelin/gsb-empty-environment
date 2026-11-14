# 伪造的虚链路把路由注入了区域

OSPF路由器接受任意端点发起的虚链路Hello，攻击者伪造LSA把流量引到自己，再以合法区域身份扩散。请实现供C++20路由协议栈调用的可导入虚链路校验组件。虚链路端点必须匹配配置的router-id与区域，Hello与LSA来源地址核对，SPF计算只接受本区域与显式信任的AS外部LSA。序列号、校验和与老化按协议校验，重复与回退LSA丢弃。用伪端點、跨区域LSA、序列回退、合法虚链路和拓扑变化验证路由不被注入。

## 组件

`netsec` 库(`netsec.hpp` / `netsec.cpp`,C++20)提供:

- `validate_hello`:虚链路 Hello 校验,router-id、transit 区域、源地址必须全部匹配 `VirtualLinkConfig`。
- `validate_lsa_wire` / `LinkStateDatabase`:LSA 老化(1..MaxAge)、Fletcher-16 校验和、序列号合法性(RFC 2328)校验;重复与回退 LSA 丢弃,仅更新的序列号替换。
- `spf_filter` / `run_spf`:SPF 只接受本区域的区域内 LSA 与显式信任的 AS 外部 LSA,跨区域与未信任外部 LSA 不进入路由计算。

## 构建与测试

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```

测试覆盖:伪端点 Hello、跨区域/未信任外部 LSA、序列回退与重复、合法虚链路建邻与路由、拓扑变化与篡改校验和。
