# netsec - FTP 数据连接策略组件

`netsec` 是供 C++20 FTP 网关导入的数据连接授权组件。它把 PASV、EPSV、PORT、
EPRT 以及代理协议提供的地址统一收敛到命令连接的真实客户端来源上，避免 FTP
bounce 把网关变成内网端口扫描器或内网服务访问代理。

## 构建与验证

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```

组件只依赖 C++20 标准库，产物为 `netsec` 静态库和公开头文件 `netsec.hpp`。

## 策略保证

- 所有数据目标地址必须位于命令连接客户端的实际来源网段。IPv4 默认 `/24`，
  IPv6 默认 `/64`，可按部署调整；PROXY 协议存在时使用受信任代理声明的客户端
  地址作为实际来源。
- 默认还要求数据目标等于客户端自身地址，而不只是同网段主机。这样被动数据
  连接只能回到客户端；需要同网段多宿主部署时才显式关闭
  `require_exact_client_address`。
- 禁止未指定地址、回环、链路本地、RFC 1918 私网、IPv6 ULA/站点本地、
  CGNAT、组播和保留地址。IPv4-mapped IPv6、IPv4-compatible IPv6、NAT64
  和 6to4 中嵌套的私网地址也会被识别并拒绝。
- 数据端口必须落在 `allowed_data_ports` 内，默认 `1024..65535`，因此 21/22
  等跳板端口默认不可用。
- 每次授权生成单次 `DataConnectionGrant`，绑定会话、期望数据对端和授权目标。
  数据连接建立后必须调用 `confirm_peer` 校验真实对端；地址或主动模式端口不
  匹配时拒绝。
- 会话和授权都由单调时钟计时。会话空闲超时、授权过期、重复授权 ID、未知会话
  和重复会话 ID 全部失败关闭。
- PROXY 头只有在提供该 TCP 连接的对端属于 `trusted_proxies` 时才被接受；默认
  不信任任何代理头。

## 网关接入流程

1. 命令连接建立时，如果启用了 PROXY 协议，用 `parse_proxy_v1` 解析并将源地址
   放入 `CommandConnection::proxied_client`；同时把代理地址网段配置到
   `PolicyConfig::trusted_proxies`。
2. 调用 `open_session(command)` 注册命令会话。重复的 `session_id` 会拒绝。
3. 收到 PASV/PORT/EPRT/EPSV 后，用对应解析函数生成 `DataRequest`，调用
   `authorize(session_id, request)`。拒绝时直接回复 5xx，不建立任何连接。
4. 被动模式只能接受与授权 `expected_peer` 匹配的数据连接；主动模式只能连接
   授权结果中的 `grant.target`，并为 `connect()` 设置连接超时。
5. 连接建立后调用 `confirm_peer(grant, peer)`。失败时立即关闭数据 socket，
   不得继续传输。
6. 命令连接关闭时调用 `close_session`；周期调用 `purge_expired` 清理过期状态。

```cpp
#include "netsec.hpp"

netsec::PolicyConfig config;
config.trusted_proxies.push_back(*netsec::Subnet::parse("10.0.0.0/8"));
config.allowed_data_ports = {1024, 65535};

netsec::DataConnectionPolicy policy(config);

netsec::CommandConnection command;
command.session_id = 42;
command.transport_peer = *netsec::Endpoint::parse("10.0.0.1:60000");
if (auto proxy = netsec::parse_proxy_v1(header_line)) {
    command.proxied_client = proxy->source;
}
if (!policy.open_session(command)) {
    // 拒绝并关闭命令连接
}

netsec::DataRequest request{netsec::DataMode::pasv,
                            *netsec::IpAddress::parse("198.51.100.10"),
                            40000};
auto authorized = policy.authorize(42, request);
if (!authorized) {
    // 回复 5xx；不要调用 connect/accept
}

// 数据 socket 建立后，peer 来自 getpeername()
if (!policy.confirm_peer(*authorized.grant, peer)) {
    // 立即关闭数据连接
}
```

## 主要 API

- `IpAddress`、`Subnet`、`Endpoint`：IPv4/IPv6 解析、比较、CIDR 归属判断。
- `parse_ftp_port_argument`、`parse_eprt_argument`、`parse_pasv_response`、
  `parse_epsv_response`：严格解析 FTP 数据地址；畸形输入返回 `nullopt`。
- `parse_proxy_v1`：解析 PROXY v1 头并校验地址族。
- `open_session`、`authorize`、`confirm_peer`、`close_session`、
  `purge_expired`：策略状态机。
- `Decision::reason` 使用 `DenyReason` 给出拒绝原因，便于审计日志。

## 测试覆盖

`tests/netsec_tests.cpp` 覆盖私网、回环、链路本地、CGNAT、IPv6 ULA、嵌套
私网地址、22 等跳板端口、同网段不同地址、跨网段地址、EPSV 地址覆盖、合法
PASV、合法 PORT/EPRT、PROXY 源替换、错误数据对端、授权重放、会话/授权超时、
会话绑定以及畸形协议解析。
