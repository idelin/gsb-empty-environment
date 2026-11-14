# netsec：图形转发一次性显示凭据

供 C++20 图形转发代理调用的访问凭据组件。它把一次显示授权绑定到
`用户 + 真实客户端地址 + 显示编号 + 连接代次`，并同时限制有效期和消费次数。
cookie 是 256 位随机 opaque token；服务端只保存 token 的 SHA-256 摘要，不把用户、
地址或显示信息编码进 cookie。

## 接入

作为子目录引入：

```cmake
add_subdirectory(netsec)
target_link_libraries(proxy PRIVATE netsec::netsec)
```

典型流程：

```cpp
#include "netsec.hpp"

netsec::AccessCredentialStore store;

// 新图形连接开始时生成单调递增的代次；真实来源地址必须来自 socket peer，
// 不能使用客户端可控的 X-Forwarded-For 等头。
const auto generation =
    store.next_generation(user, peer_address, display_number);
const auto issued = store.issue(
    {user, peer_address, display_number, generation},
    netsec::CredentialOptions{std::chrono::seconds(15)});

if (!issued) {
  reject();
  return;
}
send_cookie_to_authenticated_client(issued.credential->token());

// 客户端带回 cookie 时，在建立显示帧传输前原子兑换一次。
const auto redeemed = store.redeem(
    cookie, {user, peer_address, display_number, generation});
if (!redeemed) {
  reject(redeemed.status);
  return;
}

// 每次显示、剪贴板或文件动作前都重新检查当前代次、有效期和权限。
if (!store.authorize(*redeemed.grant, netsec::Capability::ViewDisplay,
                     display_number)) {
  reject();
  return;
}
```

显示 cookie 默认只有 `ViewDisplay`。剪贴板与文件传输必须在用户明确授权后，
由仍有效的显示会话单独签发，并且是绑定同一用户/地址/显示/代次的一次性票据：

```cpp
auto clipboard = store.issue_capability(
    *redeemed.grant, netsec::Capability::ClipboardRead,
    std::chrono::seconds(10));
auto file = store.issue_capability(
    *redeemed.grant, netsec::Capability::FileTransfer,
    std::chrono::seconds(10));
```

能力票据不能反过来签发其他能力，也不能延长到显示会话结束之后。关闭会话或
`end_connection()` 会立即撤销该会话派生的待兑换票据。

## 安全语义

- 绑定校验覆盖用户、精确客户端地址、单个显示编号和连接代次；任一不匹配都不产生授权。
- 成功兑换是原子的并发操作，只允许一个赢家；重放返回 `AlreadyUsed`。
- 绑定失败的尝试不会消费 cookie，避免攻击者借错误来源制造拒绝服务。
- 默认有效期 30 秒，`Policy::max_ttl` 默认上限 5 分钟；到期边界即失效。
- `Policy::allowed_displays` 是代理级显示白名单；每张 cookie 仍只绑定其中一个显示。
- `next_generation()` 或 `end_connection()` 使旧代次的 cookie、会话与派生票据失效。
- `authorize()` 是运行时强制点；不要只依赖 `grant.allows()` 的本地范围判断。
- 绑定失败的连接不会得到 `AccessGrant`，因此没有进入显示、剪贴板或文件传输路径的授权。
- 当前实现把待兑换凭据和活动会话保存在进程内；重启后旧 cookie 自然失效，多节点部署需共享同一存储后端或将请求固定到持有该凭据的节点。

`client_address` 必须使用代理看到的真实 peer 地址。若代理自身在另一层 TCP
转发之后，应先可靠地解析并固定该 peer，再调用本组件。

## 测试

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```

测试覆盖：合法连接、cookie 复制、跨地址连接、用户替换、显示替换、过期边界、
旧代次、显示白名单、并发重放、会话关闭、剪贴板/文件传输分离及随机 token
唯一性与不透明性。
