# netsec：核心转储策略组件

该组件供 C++20 主机守护进程导入，用来判定并执行 `core_pattern` 管道转储。
策略默认失败关闭：核心模式、路径、身份、命名空间、`suid_dumpable` 或大小任一
不满足要求，都返回拒绝，且不会回退到任何用户可控路径或第二个程序。

## 威胁模型

- `core_pattern` 和实际转储程序只来自受信任的只读配置。
- 转储程序参数只能来自静态配置字面量和数字展开；拒绝 `%e`、`%E`、`%h`、
  `%c`、`%p` 等可能带入进程名、可执行路径、主机名或命名空间局部值的展开。
- 运行期必须再次读取 `kernel.core_pattern` 并与配置完全一致；被替换即拒绝。
- 转储目录及所有父路径不得是符号链接，必须由受信任所有者持有，且不能被
  group/other 写入。
- 执行前重新打开目录和程序，比较 device/inode/owner/mode；打开目录使用
  `O_DIRECTORY | O_NOFOLLOW`。Linux 上通过 `fexecve` 执行已打开的句柄。
- 默认拒绝 root/有效 root、setuid/setgid、文件能力进程、非初始命名空间和
  非 dumpable 进程。
- 可用 `allowed_uids`、`allowed_gids` 进一步收紧崩溃进程身份。
- `fs.suid_dumpable` 与 `kernel.suid_dumpable` 为 `2` 时始终拒绝；为 `1` 时
  只有显式配置 `allow_suid_dumpable_one=true` 才允许。
- 声明大小在策略阶段检查，实际核心流在写管道时再次计数；超限立即杀死转储
  程序且不重试。
- 转储程序以绝对路径直接执行，不经过 shell，工作目录固定为 `/`，环境变量
  固定，标准输入为核心流，标准输出和标准错误为 `/dev/null`。

## 导入方式

```cmake
add_subdirectory(path/to/netsec)
target_link_libraries(hostd PRIVATE netsec)
```

```cpp
#include "netsec.hpp"

netsec::SystemEnvironment environment;
netsec::ConfigLoadOptions options;
options.trusted_uid = 0;

std::string error;
auto policy = netsec::CoreDumpPolicy::load(
    "/etc/hostd/core-policy.conf", environment, options, &error);
if (!policy) {
  // 配置不可信：不要安装或更换 core_pattern。
}

netsec::CoreDumpIdentity identity;
identity.pid = 4242;
identity.uid = 1000;
identity.gid = 1000;
identity.signal = 6;
identity.dumpable = true;
identity.core_bytes = 4096;

netsec::KernelRuntimeState runtime =
    netsec::read_kernel_runtime(environment);
netsec::PolicyDecision decision = policy->evaluate(identity, runtime);
if (!decision) {
  // 只记录拒绝原因，不执行任何替代程序。
}

netsec::PosixProcessLauncher launcher;
netsec::CoreDumpExecutor executor(*policy, launcher);
netsec::LaunchResult result = executor.execute(identity, runtime, core_fd);
```

`core_fd` 是守护进程已经接管的核转储输入流。执行计划约定：

- `argv[0]` 和 `executable` 均为配置中的绝对路径转储程序。
- 核心流从标准输入进入转储程序。
- 受信任转储程序可通过继承的目录描述符 3 写结果，即
  `CORE_DUMP_DIR_FD=3`。
- `CORE_DUMP_MAX_BYTES` 给出策略上限，便于转储程序再次约束输出大小。
- 转储程序应使用 `openat(3, name, O_CREAT | O_EXCL | O_NOFOLLOW, 0600)`
  创建输出文件，避免再次按路径解析。

守护进程必须从内核提供的崩溃元数据（`%P`、`/proc/<pid>/status`、namespace
inode 等）填充 `CoreDumpIdentity`，不得把客户端提交的字符串当作进程标识。

## 配置格式

每行一个 `key=value`，空行和 `#` 注释可忽略。配置文件和所有父路径必须是
受信任所有者持有、非符号链接且不可被非特权用户改写。

```ini
core_pattern=|/usr/libexec/hostd-core-dumper %P %u %g %s %t %d
handler=/usr/libexec/hostd-core-dumper
dump_directory=/var/lib/hostd/cores

max_core_bytes=67108864
max_pid=4194304
max_signal=64
max_dump_mode=2
max_arguments=32
max_argument_bytes=4096

allow_root_processes=false
allow_setuid_processes=false
allow_non_initial_namespaces=false
allow_suid_dumpable_one=false
allowed_uids=1000
allowed_gids=1000
```

可选命名空间白名单：

```ini
allowed_pid_namespaces=4026531836,4026532999
allowed_user_namespaces=4026531837
```

## 验证场景

`tests/test_core_dump_policy.cpp` 覆盖以下攻击面：

- `core_pattern` 被替换为攻击者管道程序。
- 配置加载后转储目录被改成可写目录、攻击者所有目录或符号链接。
- setuid/setgid/root 进程崩溃，以及 `suid_dumpable` 被放宽。
- 配置文件、转储程序、目录或父路径中的符号链接。
- 合法转储通过固定数字参数执行，且实际 fork/exec 成功。
- 核心流超过大小限制时终止且不重试。

本机运行：

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```
