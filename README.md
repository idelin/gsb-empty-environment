# netsec：cgroups v1 配置校验组件

供 C++20 容器运行时导入的 cgroups v1 安全校验库。它解决 `release_agent`
逃逸链：容器获得可写 cgroup 层级后改写 `release_agent`，再通过
`notify_on_release` 触发宿主 root 执行容器路径。

## 安全不变量

- 容器侧对 `release_agent`、`notify_on_release`、`cgroup.procs`、`tasks`
  及其他 cgroup 控制文件的写、截断、改权限、改名等操作一律拒绝。
- cgroup v1 层级挂载默认必须为只读，禁止 bind/rbind、remount、共享传播、
  未授权控制器、重复控制器和重叠挂载；非 cgroup 文件系统不得覆盖层级挂载点。
- 每个挂载与设置项都核对挂载命名空间、cgroup 命名空间、cgroup 所有权，
  设置文件必须位于委派根和 cgroup 命名空间根内。
- 锁定后的配置使用 SHA-256 指纹做创建前复核。任何变更都会通过告警回调报告，
  并拒绝创建。
- 退出事件只做验证与报告。库内没有 `fork`/`exec`/`system` 调用，`execute`
  永远为 `false`；即使 `release_agent` 在宿主可信白名单中，也只返回路径供宿主
  在独立的安全策略中处理。

## 使用

```cpp
#include "netsec.hpp"

netsec::CgroupPolicy policy;
policy.hierarchy_root = "/sys/fs/cgroup";
policy.delegated_root = "/sys/fs/cgroup/runtime/container-a";
policy.cgroup_namespace_root = policy.delegated_root;
policy.cgroup_namespace_id = observed_cgroup_ns;
policy.mount_namespace_id = observed_mount_ns;
policy.cgroup_owner_uid = 0;
policy.cgroup_owner_gid = 0;
policy.allowed_controllers = {"cpu", "cpuacct", "memory", "pids"};
policy.trusted_release_agents = {"/usr/libexec/runtime/release-helper"};

netsec::ValidationOptions options;
options.alert_sink = [](const netsec::Alert& alert) {
    log_critical(netsec::to_string(alert.code), alert.path, alert.message);
};

netsec::CgroupConfigGuard guard(observed_config, policy, options);
if (!guard.valid() || !guard.authorize_creation(observed_config, options)) {
    return refuse_container_creation();
}

const auto access = netsec::authorize_cgroup_control_write(
    request_from_container, policy, options);
if (!access.allowed) {
    reject_control_write(access.code, access.message);
}

const auto exit = netsec::evaluate_exit_event(
    release_event, policy, guard.digest(), options);
// exit.execute 恒为 false。容器路径已被拒绝并告警。
```

运行时需要从 `lstat`、`stat`、`/proc/self/mountinfo` 和 cgroup 文件系统填充
所有权、命名空间与符号链接标记。该组件验证这些观测值，不替代内核挂载顺序、
LSM 或宿主侧的最终执行白名单。

## 构建与测试

```sh
cmake -S . -B build -DCMAKE_CXX_STANDARD=20
cmake --build build --parallel
ctest --test-dir build --output-on-failure
```

安装后可导入：

```cmake
find_package(netsec CONFIG REQUIRED)
target_link_libraries(container-runtime PRIVATE netsec::netsec)
```

测试覆盖：`release_agent` 写入、路径穿越、
`notify_on_release`、相关控制文件、bind/remount/共享传播覆盖、合法与非法控制器、
所有权及命名空间不匹配、校验后配置篡改，以及退出事件不执行容器内容。
