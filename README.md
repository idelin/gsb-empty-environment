# netsec — Landlock 规则规划与运行时身份组件

这是一个嵌入 C++20 沙箱启动器的库。它把 Landlock 规则锚定在**内核对象身份**上，而不是路径字符串或用户态 uid/gid；并在 `restrict_self()` 之后为 mount namespace 变化提供带代次校验的授权钩子。

当前工作区在 macOS 上也能构建和测试：核心规划器与注入式对象身份后端是平台无关的，Linux 原生后端使用 `statx(STATX_MNT_ID_UNIQUE)`、`name_to_handle_at()` 和 Landlock syscall。非 Linux 平台的 `make_native_*()` 会返回 `UnsupportedPlatform`/`AbiUnavailable`，不会猜测或放宽权限。

## 安全不变量

- **对象身份**由 `mount_id + inode + device + inode generation + file handle` 组成。路径文本、uid、gid 和 idmap 展示身份都不参与相等判断。
- **不接受路径重解析**。调用方给出已经打开的规则根 fd；规划阶段用 `identify()`/`ancestry()` 解析层级，并用 `F_DUPFD_CLOEXEC` 保留对象。`plan()` 返回后调用方可以关闭原 fd。
- **稳定 identity 是强制要求**。原生解析器只有在拿到 `STATX_MNT_ID_UNIQUE` 或 `name_to_handle_at()` 文件句柄时才标记 `stable=true`；否则规划/应用都会失败，而不是退回路径或 uid 缓存。
- **跨 mount 的 `..` 一律拒绝**。祖先链在 mount 边界停止；若注入后端报告了跨 mount 链，规划器和钩子都返回 `CrossMountTraversal`。可选路径解析 `open_beneath()` 拒绝绝对路径、任意 `..`、中间符号链接和 magic link，并比较每一步的 mount/device。
- **ABI 只裁剪库已知且内核支持的访问位**。未知位直接 `InvalidAccessMask`；ABI 不支持的位默认 `FailClosed`，显式选择 `TrimAndReport` 时才会裁剪并记录在 `RulePlan::trimmed_access()`。ABI 查询失败、ABI=0 都失败，不会创建“全允许”的规则。
- **idmap 仅审计**。`RuleRoot::idmap` 会保存在 `PlanRule::idmaps` 中；同一 inode 在两个 idmap 视图下合并为一条规则。uid 相同但 mount 不同不会合并。
- **规划与应用之间重校验**。`apply()` 在创建 ruleset 前重新 identify/ancestry 每个保留对象；身份变化返回 `PathReplaced`，且不会调用 `create_ruleset`。
- **规则集代次在 `restrict_self()` 后生效**。`ActiveRuleset` 记录 `namespace_generation`、`ruleset_generation`（进程内单调递增）和初始 authorized roots。

## API 使用

```cpp
#include "netsec.hpp"

auto resolver = netsec::make_native_object_resolver();
auto kernel = netsec::make_native_landlock_backend();
netsec::Planner planner(resolver, kernel);

netsec::RuleRoot root;
root.fd = root_fd;                    // already opened, e.g. O_PATH
root.access = netsec::access::kReadFile |
              netsec::access::kReadDir |
              netsec::access::kRefer;
root.idmap = netsec::IdMapIdentity{.uid_inside = 1000,
                                   .gid_inside = 1000,
                                   .uid_outside = 0,
                                   .gid_outside = 0,
                                   .generation = 1};  // optional, audit only
root.label = "container-root";

netsec::PlannerConfig config;
config.mount_namespace.generation = current_mount_namespace_generation;

auto plan = planner.plan(root, config);
if (!plan) {
  // plan.error().code / plan.error().message
}

auto active = netsec::apply(std::move(plan));
if (!active) {
  // no_new_privs / landlock_create_ruleset / restrict_self failed
}

// restrict_self() has now confined the calling thread.
auto& hooks = *active->hooks;
```

之后每一个新增 mount、`move_mount` 和 `pivot_root` 都必须经过钩子。请求中的 source/destination 是**已打开的 fd**，钩子自行解析身份，不信任调用方给的路径或 uid：

```cpp
netsec::MountRequest request;
request.operation = netsec::MountOperation::Mount;
request.source_fd = source_fd;              // root of the object being mounted
request.destination_fd = new_mount_root_fd; // root of resulting mount, if available
request.namespace_generation = active->mount_namespace.generation;
request.ruleset_generation = active->ruleset_generation;
request.layout_generation = hooks.layout_generation();
request.read_only = true;

auto receipt = hooks.on_mount(request);
if (!receipt) {
  // reject the mount operation
}
```

`on_move_mount()` 和 `on_pivot_root()` 使用相同结构。成功的 bind/move 要求 destination 根与已授权 source 是同一 inode/device（mount id 可以不同）；`pivot_root` 要求 destination 就是被证明的新根。成功后会以新的 layout generation 记录 destination，后续事件必须观察到最新代次。

`pivot_root` 成功后只保留新根作为授权根；旧的兄弟子树不会继续被自动授权，避免以后用对象身份把旧子树重新挂回。只读事件的子根会标记为 read-only，后续 writable 重挂载会被 `ReadOnlyViolation` 拒绝。

注意 `landlock_restrict_self()` 是线程级的。多线程启动器必须在每个将运行不受信代码的线程上分别应用规则，或先创建线程再在线程内调用；钩子只负责 mount namespace 变化，不能替代线程级限制。

## ABI 裁剪

| ABI | 新增文件系统访问位 | 库返回的 supported mask |
| --- | --- | --- |
| 1 | `EXECUTE` … `MAKE_SYM` | `access::kAbi1` |
| 2 | `REFER` | `access::kAbi2` |
| 3 | `TRUNCATE` | `access::kAbi3` |
| 4 | 网络规则（本组件不处理 FS 新位） | `access::kAbi3` |
| 5+ | `IOCTL_DEV` | `access::kAbi3 \| kIoctlDev` |

任何库未知的位都不会被传入内核。默认 `FailClosed`；只有显式 `TrimAndReport` 才允许按 ABI 裁剪，并且 `trimmed_access` 会返回给调用方——裁剪从不静默。

## 平台与构建

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Debug
cmake --build build -j
ctest --test-dir build --output-on-failure
```

Linux 后端需要内核支持 Landlock（5.13+）。强烈建议运行在提供 `STATX_MNT_ID_UNIQUE` 或文件句柄的内核/文件系统上；否则组件按设计拒绝规划，而不是退化到不稳定的路径或 uid 缓存。

本机 macOS 上构建时需要把 `CMAKE_AR`/`CMAKE_RANLIB` 指到 CommandLineTools 的实际 `ar`/`ranlib`（工作台的 `/usr/bin/ar` 是 xcode-select shim）。测试使用注入的 fake resolver/backend 在 macOS 上覆盖 Linux 语义。

## 测试覆盖

`tests/netsec_tests.cpp` 包含：

- 同路径文本、同 inode、不同 mount id，不继承权限；
- idmap 前后 uid/gid 相同，规则对象身份不变且合并为一条规则；
- bind 子树移入、非法 destination 配对、pivot_root 后旧兄弟不再被覆盖；
- ABI 缺位、ABI 1 不支持的 `REFER`/`TRUNCATE`/`IOCTL_DEV`、未知位、默认 fail-closed 与显式裁剪；
- 两个线程同时用旧 layout generation 发起 `pivot_root`，只有一个成功，另一个 `StaleLayoutGeneration`；
- 合法只读子树被接受，后续 writable 重挂载被拒绝；
- `plan()` 与 `apply()` 之间对象被替换时拒绝创建 ruleset；
- `open_beneath()` 拒绝 `..`、绝对路径、符号链接/magic link；
- 非 Linux 原生后端的 fail-closed 行为。
