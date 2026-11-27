# Landlock 嵌套规则域策略求值器

这是一个可嵌入、零外部依赖的 C++17 头文件库，用于在用户态验证 Linux
Landlock ABI v1-v4 多层规则栈的最终权限裁决，并在测试或策略发布前审计
`REFER` 跨目录提权风险。

库不执行 `landlock_*` 系统调用，也不依赖 Linux 头文件。它把宿主准备施加的
规则集建模为不可变策略栈和目录树，供沙箱宿主做策略预演、单元测试、审计和
逃逸回归验证。目录树、路径、文件类型、TCP 端口和规则均使用标准库建模。

## 语义

- 每个 `PolicyLayer` 模拟一次 `landlock_restrict_self(2)` 施加的层。
- 对某一项权限，只有所有在 `handled_access_fs` /
  `handled_access_net` 中声明了该项的层都放行，最终裁决才放行。
- 未声明某权限的层不约束该权限；若没有任何层声明它，则该权限仍按
  环境原有权限放行。这正是漏配 `handled_access_*` 的高危点。
- 同一层内的允许规则按祖先路径/端口求并集，不同层之间求交集。子层不能
  覆盖或放宽父层限制。
- `LANDLOCK_ACCESS_FS_REFER` 是特殊权限：跨目录 `rename` / `hard link`
  要求每个层都声明并放行 `REFER`，否则按内核语义拒绝。
- 跨目录 `rename` 还要求：
  - 源父目录具备移除源对象所需的 `REMOVE_FILE` / `REMOVE_DIR`；
  - 目标父目录具备创建目标所需的 `MAKE_*`；
  - 被移动目录树在目标路径上获得的每个权限，都必须也存在于源路径上的
    权限集合中。任一子节点出现新增权限即拒绝。
- 该目标权限子集约束逐层检查：即使另一层最终否定了某个新增权限，只要
  某个单层在目标位置相对源位置放宽，操作仍按内核语义拒绝。
- `hard link` 不要求移除源，但同样要求双端 `REFER`、目标 `MAKE_*` 和
  目标权限子集约束。
- ABI v2 起提供 `REFER`，ABI v3 起提供 `TRUNCATE`，ABI v4 起提供 TCP
  `BIND` / `CONNECT` 端口规则。低 ABI 层的对应位视为不可用。
- 路径必须为绝对路径，自动折叠重复 `/` 和 `.`，拒绝 `..`。

## 快速使用

```cpp
#include "landlock_nested_policy.hpp"

using namespace landlock;

DirectoryTree tree;
tree.add_file("/secret/report.txt");
tree.add_directory("/sandbox");

PolicyLayer parent;
parent.name = "host-parent";
parent.abi_version = 4;
parent.handled_access_fs = all_fs_access();
parent.handled_access_net = NetAccess::BindTcp | NetAccess::ConnectTcp;
parent.allow_fs("/", FsAccess::Execute | FsAccess::ReadFile |
                         FsAccess::ReadDir);
parent.allow_fs("/secret", FsAccess::ReadFile | FsAccess::Refer |
                               FsAccess::RemoveFile);
parent.allow_fs("/sandbox", FsAccess::ReadFile | FsAccess::WriteFile |
                               FsAccess::MakeRegular | FsAccess::Refer);
parent.add_tcp_rule(443, NetAccess::ConnectTcp);

PolicyLayer child;
child.name = "sandbox-child";
child.abi_version = 4;
child.handled_access_fs =
    FsAccess::ReadFile | FsAccess::WriteFile | FsAccess::Refer;
child.handled_access_net = NetAccess::ConnectTcp;
child.allow_fs("/secret", FsAccess::Refer);
child.allow_fs("/sandbox",
               FsAccess::ReadFile | FsAccess::WriteFile | FsAccess::Refer);
child.add_tcp_rule(443, NetAccess::ConnectTcp);

PolicyStack stack;
stack.add(parent);
stack.add(child);

PolicyEvaluator evaluator(stack, tree);

AccessDecision read =
    evaluator.evaluate_fs("/sandbox/file.txt", FsAccess::ReadFile);
bool can_read = read.allowed;

TcpDecision connect =
    evaluator.evaluate_tcp(443, NetAccess::ConnectTcp);
bool can_connect = connect.allowed;

ReparentDecision rename =
    evaluator.check_rename("/secret/report.txt",
                           "/sandbox/report.txt");
// false: destination grants WRITE_FILE, source does not.
bool rename_allowed = rename.allowed;

AuditReport audit = audit_policy(stack, tree);
if (!audit.clean()) {
    for (const AuditFinding &finding : audit.findings) {
        // code、severity、layer、message 和可选 reparent 见证。
    }
}
```

最外层规则层先加入，子层后加入。`PolicyEvaluator` 引用策略栈和目录树，
调用方需保证其生命周期长于求值器。

## 审计

`PolicyAuditor` / `audit_policy` 会报告：

- 层 `handled_access_fs` 缺失高危文件权限，或全局没有任何层处理该权限；
- 层 `handled_access_net` 缺失 TCP 权限，或 ABI v4 层完全未处理 TCP；
- ABI 不支持却被声明/允许的权限位，以及规则权限超出
  `handled_access_*` 的必然被内核拒绝的配置；
- 规则路径不存在、端口范围倒置等建模错误；
- 跨目录 `rename` / `hard link` 的目标权限超集风险，并给出
  `ReparentWitness`：移动根、实际越权节点、源/目标权限集合、新增权限、
  以及该操作是否被 `REFER` 子树校验拦截。

审计中的提权见证表示“策略本身存在可提权组合，而求值器已用 `REFER`
子树约束阻止”。这便于做回归测试：如果未来删除或绕过子集校验，同一策略
会立即从“被拦截的见证”变成实际可提权。

`AuditOptions` 可配置高危权限集合、是否扫描跨目录提权组合，以及见证数量
上限。例如仅关心文件系统时可将 `high_risk_net` 设为 `NetAccess::None`。

## 构建与测试

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Debug
cmake --build build --parallel
ctest --test-dir build --output-on-failure
```

测试目标显式使用 `-Wall -Wextra -Wconversion -Werror -pedantic`。直接编译
嵌入代码时同样建议启用这些选项：

```sh
c++ -std=c++17 -Iinclude -Wall -Wextra -Wconversion -Werror -pedantic \
  your_sandbox_host.cpp -o your_sandbox_host
```

测试覆盖：

- 多层规则交集、同层规则并集、未声明权限默认放行、子层不能放宽；
- TCP `bind` / `connect` 的逐层端口范围收窄与 ABI v3 不可用位；
- `REFER` 双端声明、源移除、目标创建、目录子树权限子集校验；
- 缺失 `REFER` / 目标 `REFER` / 源移除权限的拒绝；
- `rename` 与 `hard link` 提权拦截及审计见证；
- 漏配 `handled_access`、ABI 不支持权限、TCP 覆盖缺失和规则一致性审计。

## 建模边界

- 目标是策略语义验证，不是内核 syscall 封装；DAC、其他 LSM、挂载点和
  路径解析竞态不在模型内。
- 目标路径按“尚不存在”处理；覆盖已存在目标的 rename 语义不在本库建模。
- 目录硬链接在 Linux 中不可用，库直接拒绝；普通文件硬链接按目标
  `MAKE_*` 与子树约束检查。
- 规则路径为普通文件时只作用于该文件自身；目录规则递归作用于后代。
