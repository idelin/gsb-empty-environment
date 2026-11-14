# 连接池把已认证状态交给了下一租户

多租户缓存代理复用后端连接，前一个租户执行AUTH后连接被放回池中，下一个租户未认证就继承了已认证状态。请实现供C++20代理调用的可导入后端连接复用组件。每条连接绑定认证主体、数据库或命名空间、权限代次与协议状态，归还时按策略重置或销毁；切换主体必须先重置认证状态并核对响应，流水线中未完成的命令不得跨租户。超时、错误和半关闭连接直接丢弃，池中连接的有效期短于服务端会话超时。用AUTH继承、跨命名空间复用、流水线残留、错误连接、合法同租户复用和重置失败验证状态不跨主体。

## 使用方式

代理侧实现 `netsec::IBackendConnection` 抽象（`netsec.hpp`），然后创建连接池：

```cpp
netsec::PoolOptions opts;
opts.reset_policy = netsec::ResetPolicy::kResetOnSwitch;
opts.max_idle_lifetime = std::chrono::seconds(60);      // 必须 < 服务端会话超时
opts.server_session_timeout = std::chrono::seconds(300);

netsec::ConnectionPool pool(opts, factory /* 建立新后端连接 */);
{
  auto lease = pool.acquire({"alice", "db0", /*perm_generation=*/1});
  auto resp = lease->request("GET key");
}  // 租约析构即归还，按策略重置或销毁
```

- 同主体、同命名空间、同权限代次：零重置直接复用。
- 跨主体 / 命名空间 / 权限代次：先 `RESET` 并核对响应与回读状态，再重新 `AUTH`/`SELECT` 绑定；任一步失败即销毁连接。
- 超时、错误、半关闭、流水线残留（`pending() > 0`）的连接归还时直接销毁。
- 构造时强制 `max_idle_lifetime < server_session_timeout`，超龄连接取出即丢弃。

## 构建与测试

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```
