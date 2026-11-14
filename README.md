# pidfd 描述符检查能力组件

该组件用于 C++20 沙箱调试代理。它把“检查时看到的 fd”与“实际复制到的 file
description”绑定为同一个内核身份，避免目标在检查和 `pidfd_getfd(2)` 之间
`exec`、close/reopen 或切换命名空间后复制到新安全域中的文件。

实现不读取 procfs 的进程 fd 目录。默认复制路径直接调用
`pidfd_getfd(2)`，并在新描述符上设置 `FD_CLOEXEC`；身份检查、枚举、冻结和
事件由注入的 `netsec::pidfd::Kernel` 提供。

## 安全不变量

- `ProcessIdentity` 同时比较 pidfd cookie、进程启动时间、exec generation、
  user namespace、mount namespace 和 policy domain。PID 号不是身份。
- `FileDescriptionIdentity.description_token` 标识 file description
  (`struct file`) 本身而不是 inode；close/reopen 同一路径必须产生新 token。
  `slot_generation` 另外覆盖 fd 表槽位复用。
- 枚举结果记录 fd 号、稳定 file description 身份、访问模式和允许操作。
- 复制前调用 `probe_fd()`，复制后再调用一次；另由
  `checked_duplicate()` 或 `verify_copied_fd()` 证明实际返回的描述符身份。
- `supports_atomic_checked_duplicate() == false` 时，只允许在
  `freeze()`/`thaw()` 支持的窗口内复制。既不能原子校验又不能冻结时直接
  返回 `Status::UnstableKernelInterface`，不会调用复制。
- `exec`、`setns`、凭据变化、fd close/reopen、fd 表变化和 pidfd 退出事件
  都会撤销快照；安全域事件还会立即关闭已交付的代理句柄。
- 复制期间发生事件或身份变化时，已经拿到的副本由 `UniqueFd` 立即关闭，
  接口返回 `StaleSnapshot`/`IdentityMismatch`，不会交给调用方。
- `RestrictedHandle` 不提供裸 fd，不提供 `write`、`fcntl`、`mmap` 等操作，
  只允许对当前文件 description 做位置化 `read_at()`。可写源 fd 始终拒绝，
  因为 `pidfd_getfd(2)` 不能把可写描述符降级为只读。
- 事件订阅是打开组件的必要条件；没有事件通道的后端会被直接拒绝，不能
  通过配置关闭撤销语义。

## 公共 API

```cpp
#include "netsec.hpp"

using namespace netsec::pidfd;

auto agent = CapabilityAgent::open(kernel_backend, UniqueFd(pidfd_fd));
if (!agent) {
  return agent.status();
}

auto snapshot = (*agent)->enumerate();
if (!snapshot) {
  return snapshot.status();
}

auto handle = (*agent)->duplicate_read_only(42);
if (!handle) {
  return handle.status();
}

std::byte buffer[4096];
auto count = handle.value().read_at(0, buffer);
```

`CapabilityAgent::duplicate_read_only_async()` 提供异步版本。若在复制完成前
目标退出或发生安全域事件，future 返回失败，底层迟到描述符已经被关闭。

## 注入内核契约

`Kernel` 是最小可信面。生产后端通常位于特权 broker 或经过加固的内核
辅助接口中，必须保证：

1. `inspect_process()`、`enumerate_fds()`、`probe_fd()` 返回的 token
   单调唯一且不可伪造，并来自 pidfd 所指的实际进程。
2. `checked_duplicate()` 在**一个**内核操作内比较完整 `ExpectedFdBinding`
   并复制同一个 fd。返回的 `CopyWitness` 必须描述实际复制对象。
3. 无法原子复制时，`freeze()` 必须冻结所有共享 fd 表的线程；`thaw()`
   必须可靠恢复。
4. `subscribe_events()` 必须可靠投递下列事件；`unsubscribe_events()`
   返回前必须停止回调。
5. `verify_copied_fd()` 应校验代理进程中的副本，返回其 file description
   身份。默认 `duplicate_fd()` 只有在 `verify_copied_fd()` 成功后才返回
   副本；未实现该校验的后端只会得到 `Unsupported`。

原子接口的推荐语义可概括为：

```text
compare(pidfd, start_time, exec_generation,
        user_ns, mount_ns, policy_domain,
        fd_slot_generation, description_token)
        && pidfd_getfd(pidfd, fd)
```

如果内核不能提供上述事务，组件在用户态执行：

```text
freeze(pidfd)
probe(pidfd, fd)
pidfd_getfd(pidfd, fd)
probe(pidfd, fd)
verify(copied_fd)
thaw(pidfd)
```

任何一步失败都会关闭副本并拒绝返回句柄。

## 构建与验证

```sh
cmake -S . -B build -DCMAKE_CXX_STANDARD=20
cmake --build build
ctest --test-dir build --output-on-failure
```

测试内核模拟器覆盖：

- `exec` 后同一 fd 号指向新安全域文件；
- pidfd 号相同但 PID/启动时间复用；
- 跨 mount namespace；
- close/reopen 与复制并发（非原子接口必须拒绝或冻结）；
- 目标在复制中途退出；
- 复制结果迟到后立即关闭；
- fd 事件撤销已交付句柄；
- 合法只读源的身份恒等与内容读取；
- 可写源和缺少原子/冻结能力的失败关闭路径。
