# RADIUS 漫游属性可信转发组件

这是一个 C++20、无外部依赖的 RADIUS 属性转发与归属判定库，面向漫游代理和归属服务器。它解决的核心问题是：客户端可写属性（尤其是运营商/VSA 属性和伪造的 `Proxy-State`）不能参与归属策略，策略只能来自代理签名并完整回显的可信链。

## 安全模型

- 每次转发在原始属性之后追加一个 `Proxy-State`，不重排、不丢弃客户端属性和上游 `Proxy-State`。
- `Proxy-State` 由 128 位操作系统 CSPRNG 随机 nonce、请求摘要、上游链摘要、路由承诺、共享密钥代次和 HMAC-SHA256 组成，代理之间不可伪造。
- 响应中的 `Proxy-State` 值必须与请求链逐项、逐字节、同序完全一致；缺项、多项、重排、篡改或剥离都会被拒绝。响应还使用 RADIUS Response Authenticator 校验。
- 客户端可写属性与代理生成属性在 `ForwardedRequest` 中分区保存。归属服务器只读取已验证链中的来源和已签名路由承诺，不读取客户端 `Operator-Name`、3GPP/Wi-Fi VSA 等运营商属性。
- 每个转发跳追加的 `Proxy-State` 绑定前一跳链摘要；归属服务器验证直接对端签名、代次和前序链摘要，因此合法两跳漫游可以传递信任，但攻击者不能在可信状态之后插入自己的状态。
- 归属策略同时按 `NAS-Identifier`、来源共享密钥代次、漫游对端身份、本跳签名密钥代次和路由承诺匹配。未知、已退役或未配置的代次一律拒绝。
- `authorize()` 和 `account()` 调用完全相同的归属判定函数，不会因为报文是 Access-Request 或 Accounting-Request 而产生不同来源结论。
- RADIUS 解析严格校验报文长度、属性边界、固定长度属性、`Class` 等可变长度属性的范围、VSA 的 `Vendor-Id`/`Vendor-Type`/`Vendor-Length`，并拒绝重复的单值属性。`Proxy-State`、`Class`、EAP-Message 和 Reply-Message 是显式可重复属性。

## 构建与测试

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build -j
ctest --test-dir build --output-on-failure
```

安装后可作为 CMake 包导入：

```sh
cmake --install build --prefix /path/to/prefix
```

```cmake
find_package(netsec CONFIG REQUIRED)
target_link_libraries(my_radius_proxy PRIVATE netsec::netsec)
```

## 使用方式

代理侧配置来源绑定、共享密钥代次和路由：

```cpp
using namespace netsec::radius;

KeyRing proxy_keys;
proxy_keys.install("nas-1", 1, nas_secret);
proxy_keys.install("proxy-b", 1, proxy_to_b_secret);

std::vector<SourceBinding> sources{
    {"nas-1", IngressKind::Nas, "NAS-01", 1, "roam-op"},
};
std::vector<PolicyRoute> routes{
    {SourceIdentity{"NAS-01", 1, "roam-op"},
     "home-c", "billing-gold",
     {Attribute{126, {'r','o','a','m','-','o','p'}}},
     "proxy-b", 1},
};

ProxyEngine proxy("proxy-a", proxy_keys, sources, routes);
ForwardedRequest out = proxy.forward(request_wire, {"nas-1", true});
// 把 out.wire 发给下一跳；out.proxy_state_chain 是必须回显的链。
```

收到响应后：

```cpp
VerifiedResponse response = proxy.accept_response(response_wire, out);
// response.message      完整校验后的响应
// response.upstream_wire 已去掉本跳 Proxy-State 并重算认证的上游响应
```

归属服务器侧：

```cpp
HomeServer home("home-c", home_keys, home_policies);
PolicyDecision auth = home.authorize(out.wire, {"proxy-b", true});
PolicyDecision acct = home.account(accounting_from_b.wire, {"proxy-b", true});
// auth.home_server / auth.billing_group / auth.trusted_operator_attributes
// 只来自已验证的 Proxy-State 路由承诺和本地策略。
```

## 测试覆盖

`tests/netsec_tests.cpp` 覆盖：

- 剥离 `Proxy-State` 和攻击者追加伪造 `Proxy-State` 均被拒绝；
- 客户端追加伪造 `Operator-Name`、3GPP VSA 计费组和归属 VSA 时，策略仍只采用可信路由；
- 重复 `NAS-Identifier`、`Operator-Name`、配置为单值的 VSA，以及固定长度/VSA/`Class` 长度错误均被拒绝；
- 响应链缺失、重排、篡改均被拒绝；
- 共享密钥旧代次退役、新代次启用和跨代次拒绝；
- 合法两跳漫游的请求与响应完整往返，且授权与计费归属完全一致；
- MD5/HMAC-MD5/SHA-256 与独立 hashlib 向量一致。

## 集成注意

- `KeyRing`、`ProxyEngine` 和 `HomeServer` 是值语义配置对象；并发调用前请由调用方保证配置不再被并发修改。
- 生产环境应将 `IngressInfo::transport_peer` 来自可信的传输层对端身份（例如 TLS 客户端证书或受控源地址），而不是报文内字段。无 `Message-Authenticator` 的 Access-Request 必须使用已认证传输绑定。
- 组件不负责 RADIUS 重放缓存、密钥持久化或远端密钥分发；这些需要与部署环境的密钥管理和重放防护配合。
