# 远程桌面认证降级

远程桌面服务允许客户端协商认证类型，攻击者强制降级到旧式认证并中间人截获口令。请实现供C++20远程桌面服务端调用的可导入认证协商组件。策略要求网络级认证时拒绝降级，协商结果绑定服务端配置、证书与协议版本；弱认证仅在显式允许的隔离网络使用并记录。CredSSP与票据绑定会话。用类型降级、证书替换、版本回退、合法强认证和显式弱策略验证口令不出现在明文通道。

## 组件说明

`rdp_auth` 是一个零依赖的 C++20 静态库（命名空间 `rdp::auth`），供远程桌面服务端导入完成认证协商：

- `rdpauth/negotiator.hpp` — `Negotiator`：协商引擎，强制 NLA 反降级、版本回退与证书替换防护，弱认证仅限显式允许的隔离网段并写入审计
- `rdpauth/channel.hpp` — `SecureChannel`：仅由已接受且绑定可验证的协商结果建立，口令只以加密帧上线
- `rdpauth/types.hpp` — 策略、请求、决策、`SessionBinding`、`CredSspTicket`、`AuditEvent`
- `rdpauth/crypto.hpp` — 内置 SHA-256 / HMAC-SHA256 / 常数时间比较

### 安全不变量

- `require_nla` 为真时，任何低于 CredSSP 的请求一律拒绝（弱认证许可不得覆盖）
- 客户端版本低于 `min_protocol_version` 拒绝；客户端所见证书指纹与配置不符拒绝
- 接受的协商产生 `SessionBinding`，其 HMAC 令牌绑定服务端配置哈希、证书指纹、协议与版本
- CredSSP 票据 MAC 绑定签发会话，跨会话、过期或篡改均验证失败
- 弱认证需 `allow_weak_auth` 显式开启且客户端位于 `isolated_networks`，并接受审计记录

### 导入方式

```cmake
add_subdirectory(path/to/rdp_auth)
target_link_libraries(your_rdp_server PRIVATE rdp::auth)
```

或 `cmake --install` 后通过 `find_package` 使用导出的 `rdp::rdp_auth` 目标。

### 构建与测试

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build
ctest --test-dir build --output-on-failure
```

测试覆盖：类型降级、证书替换、版本回退、合法强认证（含绑定/票据校验）、显式弱策略，以及跨场景口令不出现在明文通道的断言。
