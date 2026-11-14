# 命名空间内可以跟踪外部进程

沙箱创建user namespace后用ptrace跟踪宿主进程，Yama限制按命名空间层级计算失效，攻击者读取宿主内存。请实现供C++20沙箱运行时代用的可导入跟踪权限组件。ptrace关系按真实用户、命名空间祖先与capability集合共同判定，子命名空间不得获得对其祖先命名空间进程的跟踪权；dumpable与no_new_privs状态一并复核。审计记录调用链。用跨命名空间跟踪、关系伪造、dumpable变化、合法同命名空间跟踪和策略收紧验证不越层。

## 组件说明

`netsec` 库提供可导入的 ptrace 跟踪权限判定组件（C++20）：

- `netsec::UserNamespaceRegistry`：唯一持有命名空间树的注册表，调用方只能按 id 引用，父链不可伪造；同时维护权威的 pid→命名空间隶属关系。
- `netsec::TracePermissionEvaluator`：按真实 uid、目标命名空间祖先链上的 capability 集合共同判定；capability 仅在目标命名空间或其祖先中持有才有效，子命名空间内的特权不会向上传播。每次判定都复核 dumpable 与 no_new_privs（`TracePolicy` 可收紧）。
- `netsec::AuditLog`：追加式哈希链审计日志，记录每次判定所走的命名空间调用链，`verify_chain()` 可检测篡改。

构建与测试：

```sh
cmake -S . -B build -DCMAKE_AR=/Library/Developer/CommandLineTools/usr/bin/ar \
      -DCMAKE_RANLIB=/Library/Developer/CommandLineTools/usr/bin/ranlib
cmake --build build
ctest --test-dir build --output-on-failure
```

测试覆盖：跨命名空间跟踪、关系伪造、dumpable 变化、合法同命名空间跟踪、策略收紧与审计篡改检测（`tests/trace_permission_tests.cpp`）。
