# FUSE 节点与请求生命周期守卫

这是一个不依赖 libfuse 的 C++20 状态组件。守护进程在发送 `FUSE_LOOKUP`
回复前记录节点身份，在分发普通请求时创建一次性 ticket，并在后端执行任何
可见副作用前调用 `Request::before_side_effect()` 做最终复核。

## 身份模型

每个 `LookupResult` 同时绑定：

- mount id 和连接 epoch（`SessionKey`）
- kernel node id 与 per-incarnation `generation`
- 当前内核 lookup 引用数
- 后端稳定对象 id（`StableObjectId`）
- 策略代次 `PolicyGeneration`
- 目录名、dentry revision 和 cache epoch

node id 只有在 lookup 引用、在途 ticket、通知引用和开放句柄全部归零，
且隔离时间结束后才能被另一个对象复用。复用会分配新的 generation；旧
generation 的请求只能得到 `nodeid-reused` 见证，不能产生副作用。

`FORGET` 和 `BATCH_FORGET` 仅返回内部计数结果，不会生成 FUSE 响应。

## API 概览

```cpp
#include "netsec.hpp"

netsec::LifecycleOptions options;
options.quarantine = std::chrono::seconds(1);
netsec::NodeLifecycle lifecycle(options);

auto session = lifecycle.open_session(7);
auto node = lifecycle.lookup(session, 0, "backend-object-17", 4,
                             {1, "report"});

auto ticket = lifecycle.receive_request(
    {session, node.entry->nodeid, node.entry->generation},
    9001, {1000, 1000, 4242, "sandbox"});

if (ticket.ok() && ticket.request->before_side_effect().ok) {
    // 此时才允许修改后端对象或返回用户数据。
    ticket.request->complete();
}
```

关键入口：

- `lookup`：分配或复用 node id，并建立完整身份记录。
- `forget` / `batch_forget`：只减少 lookup 引用，归零后启动隔离计时。
- `receive_request`：快照 generation、稳定对象 id、策略代次和凭据。
- `Request::validate` / `before_side_effect`：重核会话、generation、对象、
  策略、目录名和凭据。
- `interrupt` / `interrupt_ticket`：只结算指定 unique 的最旧 ticket；迟到
  完成按内部 ticket，而不是按可复用的数字 unique 结算。
- `abort_session` / `restart_session`：旧 epoch 的所有 ticket 立即失效。
- `rename_exchange`：只交换目录项绑定；node id、generation 和稳定对象 id
  不变。
- `unlink` / `open_handle`：unlink 后句柄仍保有对象身份，句柄存在期间禁止
  node id 复用。
- `notify_inval_inode`：增加通知引用并推进 cache epoch；失效不改变身份，
  通知完成后才解除对复用的阻塞。
- `first_identity_conflict`：返回组件观察到的首个 `IdentityWitness`。

## 构建与验证

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```

`netsec_lifecycle_test` 用延迟后端交错 FORGET、node id 复用、
RENAME_EXCHANGE、INTERRUPT/unique 复用、连接重启、开放句柄、通知失效和
合法缓存命中。`netsec_identity_probe` 输出首个身份冲突见证，例如：

```text
sequence=2 kind=nodeid-reused op=request-receive ... generation=1->2 object=object-a->object-c
```
