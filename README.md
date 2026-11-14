# netsec：受信的 udev 规则加载组件

`netsec` 是供 C++20 设备管理器调用的规则加载库。它解决的问题是：配置目录一旦可写，普通用户就可以放入新 `.rules` 文件，再伪造/触发设备事件，使辅助程序以 root 执行。

组件把规则文件视为不可信输入，并在发布规则前完成所有权、权限、语法、命令目录、参数模板和环境变量校验。设备事件只会绑定到一个不可变规则快照；重载成功后原子切换，失败则继续使用旧规则和旧代次。

## 构建与测试

```sh
cmake -S . -B "$TMPDIR/netsec-build" -DCMAKE_CXX_COMPILER=clang++
cmake --build "$TMPDIR/netsec-build"
ctest --test-dir "$TMPDIR/netsec-build" --output-on-failure
```

测试在 `TMPDIR` 下创建专属规则目录，覆盖：

- 规则目录或规则文件可写时拒绝加载；
- `RUN` 路径/参数注入和 shell 替换拒绝；
- 规则内环境变量赋值及非白名单 `ENV{}` 扩展拒绝；
- 伪造 `ACTION`、设备路径、属性或环境值时整事件拒绝；
- 合法规则只产生宿主登记的命令；
- 热重载按代次原子切换，坏规则重载失败保留旧规则。

## API 使用

命令目录由宿主配置，规则文件只能选择命令，不能决定可执行文件或拼接任意 argv。

```cpp
#include "netsec.hpp"

netsec::CommandPolicy helper;
helper.name = "device-helper";
helper.executable = "/usr/libexec/device-helper";
helper.argument_template = {
    "--action", "{ACTION}",
    "--kernel", "{KERNEL}",
    "--serial", "{ENV:SERIAL}",
};
helper.environment_template = {
    {"DEVICE_ACTION", "{ACTION}"},
    {"DEVICE_SERIAL", "{ENV:SERIAL}"},
};

netsec::RuleFilePolicy file_policy;
file_policy.trusted_uid = 0;  // root daemon 的默认值

netsec::RuleEngine engine("/etc/device-manager/rules.d", {helper},
                          file_policy);
auto loaded = engine.load();

netsec::DeviceEvent event;
event.action = "add";
event.subsystem = "usb";
event.kernel = "sda1";
event.devname = "/dev/sda1";
event.environment["SERIAL"] = "SN-42";

auto evaluation = engine.evaluate(event);
for (const auto& command : evaluation.commands) {
    // 仅将这里返回的 CommandInvocation 交给 execve/posix_spawn。
    // argv 已固定来自 helper.argument_template，environment 为干净白名单。
}
```

`evaluate()` 只返回受信命令，不直接启动进程。设备管理器应使用 `argv`、`executable` 和独立的 `environment` 执行，且不要经过 shell。`Evaluation::generation` 与每条 `CommandInvocation::generation` 都指出事件绑定到的快照代次。

若事件需要排队，可先保存快照，再延迟绑定：

```cpp
auto snapshot = engine.snapshot();
auto evaluation = engine.evaluate(event, *snapshot);
```

## 规则语法

每行一条规则，使用逗号分隔的条件，最后必须有一个 `RUN`：

```udev
# 合法示例
ACTION=="add", SUBSYSTEM=="usb", KERNEL=="disk[0-9]", ENV{SERIAL}=="SN*", RUN+="device-helper"

# 参数模板兼容形式；参数必须与 CommandPolicy 逐字一致
ACTION=="add", RUN+="/usr/libexec/device-helper --action {ACTION} --kernel {KERNEL}"
```

允许的匹配字段只有：

- `ACTION`、`SUBSYSTEM`、`KERNEL`、`DEVPATH`、`DEVNAME`、`DRIVER`、`SYSNAME`、`SYSPATH`、`SEQNUM`；
- `ATTR{name}`；
- `ENV{name}`，但 `name` 必须存在于 `RuleFilePolicy::allowed_event_environment`；
- `RUN`，必须使用 `+=`，且每行只能出现一次。

匹配操作符仅允许 `==` 和 `!=`。字符串值可包含简单 glob（`*`、`?`、`[a-z]`），但拒绝引号、反斜杠、shell 元字符、注释符和空白。未知关键字、`PROGRAM`、`IMPORT`、`GOTO`、`LABEL`、规则内 `ENV{...}=` 赋值等均拒绝。

## 安全约束

- **文件信任**：规则目录链上的目录必须由 root 或配置的 `trusted_uid` 所有，且 group/other 不可写。仅系统级 sticky 父目录（如 `/tmp` 的 `1777`）可作为祖先例外，规则目录本身仍必须不可写。`.rules` 必须为普通文件，由 root 或受信服务账号所有，且所有者、组、其他用户均无写位；符号链接、非普通文件和超限文件拒绝。
- **安全读取**：使用目录 fd + `openat(..., O_NOFOLLOW)`、`fstat` 和大小上限读取，避免跟随符号链接或读取被替换的路径。
- **语法白名单**：只解析上述字段、操作符和值字符集，不做 udev 的任意脚本/导入/跳转语义。
- **固定命令**：`RUN` 首选引用 `CommandPolicy::name`。若使用绝对路径兼容形式，路径、参数数量和参数模板必须与宿主策略逐字匹配。规则文本不会成为可执行文件或命令行。
- **受控环境**：子进程环境不从守护进程继承，也不是规则可赋值的 map；只由 `CommandPolicy::environment_template` 构造。来源 `ENV{}` 键受白名单限制，输出变量名拒绝 `PATH`、`LD_*`、`DYLD_*`、`BASH_ENV` 等注入变量。
- **事件校验**：`ACTION` 必须是已知 udev 动作；设备字段和受控环境值不得包含控制字符、空白、shell 元字符或越界长度。只要一条展开失败，整个事件不产生任何命令。
- **代次绑定**：每次 `evaluate()` 在一次加锁中取得一个 `shared_ptr<const RuleSet>`，随后完全基于该不可变快照求值。规则文件重载先完整解析校验，再在锁内一次替换指针；失败时 `activeGeneration()` 和旧规则不变。

默认 `trusted_uid = 0`，适用于 root 守护进程。若规则由其他受信系统服务账号管理，可设置 `RuleFilePolicy::trusted_uid` 为该账号 uid；测试环境也使用该字段显式表达信任边界。
