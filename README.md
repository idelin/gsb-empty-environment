# netsec：可导入的权限责任归属组件

`netsec` 是一个只依赖 C++20 标准库的权限代理后端。它解决“宿主应用拿到相机/文件权限后，插件或子进程直接复用宿主授权”的问题：授权不再属于 PID 或宿主应用，而是绑定到一次明确的可审计请求。

## 安全不变量

每次访问必须同时匹配以下绑定，少一项都会拒绝：

- 签名主体：稳定的代码签名主体（`subject.id` + `signer`），不是进程名或 PID。
- 审计令牌：由 `PermissionBroker::issue_audit_token()` 服务端签发并保存，调用方修改令牌字段不会改变其绑定。
- 进程代次：`ProcessId + Generation` 精确匹配；PID 复用或进程重启后旧授权失效。
- 用途：授权和访问检查必须给出相同的非空 `purpose`。
- 进程种类与执行模式：宿主、子进程、插件、后台任务分别绑定；子进程/插件不会自动继承任何父授权。

额外约束：

- 子进程/插件重新请求时必须提供调用链；签发结果和审计记录都会保留该链。
- 跨主体的父授权只能作为“来源”出现在调用链中，签发的新授权仍属于子进程/插件自己的签名主体，不能复用宿主 grant。超过父授权权限集的请求会被拒绝。
- 临时授权必须带用途和过期时间，到期后立即拒绝；临时授权的 TTL 受策略上限约束。
- 后台任务与前台授权分开评估：前台 grant 不能用于后台上下文，反之亦然。
- 所有签发、放行、拒绝、撤销和代次推进都会写入内存审计轨迹。

## 基本用法

```cpp
#include "netsec.hpp"

using namespace netsec;
using namespace std::chrono_literals;

PermissionBroker broker;

auto host = make_signed_subject("com.example.host", "Team-A", "sig:host");
auto host_token = broker.issue_audit_token(host, 1000, 1);

RequestContext host_context;
host_context.subject = host;
host_context.audit_token = host_token;
host_context.process_id = 1000;
host_context.generation = 1;
host_context.process_kind = ProcessKind::HostApplication;

PermissionRequest host_request;
static_cast<RequestContext&>(host_request) = host_context;
host_request.permissions = {Permission::Camera, Permission::FileRead};
host_request.purpose = "camera-preview-and-file-import";
host_request.ttl = std::chrono::seconds(300); // 可选：临时授权

auto issued = broker.request(host_request);
if (!issued) {
    // issued.reason / issued.detail 说明拒绝原因
}

auto access = broker.check(issued.grant->id, host_context, Permission::FileRead,
                           host_request.purpose);
if (access) {
    // 允许访问
}
```

插件不能直接使用宿主 grant：

```cpp
auto plugin = make_signed_subject("com.example.plugin", "Team-B", "sig:plugin");
auto plugin_token = broker.issue_audit_token(plugin, 2000, 1);

RequestContext plugin_context;
plugin_context.subject = plugin;
plugin_context.audit_token = plugin_token;
plugin_context.process_id = 2000;
plugin_context.generation = 1;
plugin_context.process_kind = ProcessKind::Plugin;

// 拒绝：PluginInheritanceDenied
auto denied = broker.check(issued.grant->id, plugin_context, Permission::FileRead,
                           host_request.purpose);

// 插件必须重新请求；调用链会展示宿主加载插件的过程。
PermissionRequest plugin_request;
static_cast<RequestContext&>(plugin_request) = plugin_context;
plugin_request.permissions = {Permission::FileRead};
plugin_request.purpose = "plugin-import";
plugin_request.parent_grant_id = issued.grant->id; // 仅作为来源，不是继承
plugin_request.call_chain = {
    CallChainEntry{.subject_id = host.id,
                   .signer = host.signer,
                   .operation = "host-load-plugin",
                   .process_id = 1000,
                   .generation = 1,
                   .process_kind = ProcessKind::HostApplication,
                   .grant_id = issued.grant->id,
                   .purpose = "camera-preview-and-file-import"},
    CallChainEntry{.subject_id = plugin.id,
                   .signer = plugin.signer,
                   .operation = "plugin-request",
                   .process_id = 2000,
                   .generation = 1,
                   .process_kind = ProcessKind::Plugin,
                   .purpose = "plugin-import"},
};

auto plugin_grant = broker.request(plugin_request);
// plugin_grant.grant->subject 是插件自己的签名主体。
// 请求 FileWrite 会得到 PermissionEscalation。
```

进程代次变化或撤销：

```cpp
broker.advance_generation(1000, 2); // 旧代次 grant 与 token 立即失效
broker.revoke(issued.grant->id);
auto trail = broker.audit_trail();  // 责任归属记录
```

## 关键 API

| API | 作用 |
| --- | --- |
| `issue_audit_token(subject, pid, generation)` | 签发与签名主体、PID、代次绑定的 opaque token；返回空 token 表示主体无效或代次已过期。 |
| `request(...)` / `requestAccess(...)` | 显式请求一次新授权；不会复制父授权。 |
| `check(grant_id, context, permissions, purpose)` | 校验指定 grant 的全部绑定。 |
| `authorize(context, permissions, purpose)` | 在当前 broker 中按精确上下文查找可用 grant。 |
| `advance_generation(pid, generation)` | 推进进程代次并撤销旧代次 grant。 |
| `revoke` / `revoke_subject` / `revoke_process` | 撤销单条、主体或进程的全部授权。 |
| `audit_trail()` / `format_call_chain()` | 读取审计记录并展示调用链。 |

`Decision.reason` 使用 `DenialReason` 区分插件继承、子进程继承、代次不匹配、主体不匹配、令牌不匹配、用途不匹配、过期、提权等拒绝原因。

## 构建、测试与导入

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build --parallel
ctest --test-dir build --output-on-failure
```

作为子目录导入：

```cmake
add_subdirectory(path/to/netsec)
target_link_libraries(your_permission_proxy PRIVATE netsec::netsec)
```

安装后也可以使用：

```cmake
find_package(netsec CONFIG REQUIRED)
target_link_libraries(your_permission_proxy PRIVATE netsec::netsec)
```

测试覆盖：宿主授权绑定、插件继承、子进程提权、代次变化、合法重新授权、用途不匹配、临时授权过期、后台任务隔离、跨主体/伪造令牌、撤销与审计轨迹。

## 边界说明

该组件是权限代理的授权与归属判定层，不替代操作系统沙箱。签名默认要求 `id`、`signer`、`signature` 非空；生产环境应通过 `SubjectVerifier` 接入平台代码签名验证。`request()` 应由受信任的授权 UI/代理调用，插件进程不应直接持有 broker 的签发能力。
