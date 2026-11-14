# 解析器连接被多个租户共用

## 组件说明

`netsec` 库为 C++20 DoT 网关提供可导入的上游连接组件：

- `ConnectionKey`（`netsec.hpp:23`）绑定解析器身份、租户、SNI 与策略代次；`ConnectionPool` 严格按 key 分发连接，任一字段不同即新建会话，跨租户复用在构造上不可能。
- `UpstreamConnection`（`netsec.hpp:118`）在连接内分配唯一的在途查询 ID，响应仅当 ID 与问题同时匹配时才被接受。
- `TenantRegistry` / `TenantContext`（`netsec.hpp:96`）按租户隔离超时策略与响应缓存，不存在全局缓存。
- `sanitize_edns`（`netsec.hpp:62`）剥离 ECS、私有区间及网关标记为携带身份的 EDNS 选项。
- 传输层由网关通过 `ITransport` / `TransportFactory` 注入；`on_transport_closed()` 使在途查询失败并换新会话，不做隐式重放。

## 构建与测试

```sh
cmake -S . -B build && cmake --build build
cd build && ctest --output-on-failure
```

测试覆盖：跨租户复用、ID 冲突、缓存混用、合法同租户查询、超时与重连、EDNS 身份剥离（`tests/test_netsec.cpp`）。

DNS over TLS网关复用上游连接，不同租户查询共用同一会话，攻击者通过连接状态或缓存推断他人查询。请实现供C++20 DoT网关调用的可导入上游连接组件。连接绑定解析器身份、租户、SNI与策略代次，跨租户不得复用；查询ID在连接内唯一，响应按ID与问题匹配。超时、重连与缓存按租户隔离，EDNS选项不透传身份。用跨租户复用、ID冲突、缓存混用、合法同租户查询和重连验证查询不串租户。
