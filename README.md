# 特权服务环境文件加载组件

`netsec.hpp` 提供可由 C++20 服务管理器直接链接使用的
`netsec::EnvironmentFileLoader`。它不会调用 `setenv()`，也不会执行环境文件
中的内容；调用方只使用成功返回的不可变快照来构造受控子进程环境。

## 使用

```cpp
#include "netsec.hpp"

#include <iostream>

int main() {
    netsec::EnvironmentPolicy policy;
    policy.trusted_owner = 0;  // root 持有生产环境文件
    policy.allowed_variables = {"TZ", "LOG_LEVEL", "SERVICE_MODE"};

    netsec::EnvironmentFileLoader environment(
        "/etc/my-service/service.env", policy);

    try {
        const netsec::EnvironmentSnapshot snapshot = environment.load();
        // 仅把 snapshot.variables 交给受控 exec，并记录
        // snapshot.inode / snapshot.sha256 / snapshot.generation。
    } catch (const netsec::EnvironmentException& error) {
        std::cerr << "refusing to start: " << error.what() << '\n';
        return 1;  // 启动阶段必须失败，绝不使用部分结果
    }
}
```

重新加载时再次调用 `load()`。它会重新打开路径并执行全部属主、权限、符号链接、
大小、白名单和内容校验。失败时抛出 `EnvironmentException`，`current()` 仍是
上一次成功加载的完整快照；服务可在运行期保留旧快照，但启动阶段必须直接失败。

`EnvironmentSnapshot` 包含：

- 完整变量表 `variables`
- 文件设备号与 inode `device` / `inode`
- 文件大小、属主和模式 `size` / `owner` / `mode`
- 原始字节的 SHA-256 十六进制摘要 `sha256`
- 代次 `generation`

首次成功加载为第 1 代。只有设备号、inode、大小、属主、模式或哈希发生变化且
新文件通过全部校验时，代次才加 1；未变化的重复加载保持原代次。

## 校验规则

- 文件必须是普通文件，属主必须等于 `trusted_owner`（未设置时使用
  `geteuid()`），且不得有组写或其他用户写权限。
- 路径的每个组成部分都不得是符号链接。正常路径使用逐级
  `openat(O_NOFOLLOW|O_DIRECTORY)`；所有内容读取和权限判断都绑定到已打开的
  文件描述符，并在读取前后比较设备号、inode、大小、属主、模式和纳秒时间戳。
- 变量采用严格的 `NAME=VALUE` 格式，名称仅允许
  `[A-Za-z_][A-Za-z0-9_]*`；名称和值两侧的水平空白会被去除，之后按字面量
  保存，不做 shell 展开、命令替换或引号解释。空行和以 `#` 开头的注释行可
  忽略；重复变量直接失败。
- 默认单行最多 4096 字节、文件最多 64 KiB，均可通过
  `EnvironmentPolicy::max_line_bytes` / `max_file_bytes` 收紧或调整。
- 变量必须精确出现在 `allowed_variables` 白名单中。`LD_*`、`DYLD_*`、
  `LD_PRELOAD`、`PATH`、`PYTHONPATH`、`BASH_ENV`、`NODE_OPTIONS` 等动态
  链接、搜索路径和解释器/启动器类变量始终被拒绝，即使误加入白名单也不会加载。
- 任一解析错误都会让整次加载失败，不会保留已解析的前半部分。

## 构建与测试

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Debug
cmake --build build
ctest --test-dir build --output-on-failure
```

测试使用 `TMPDIR` 下的临时目录，覆盖：

- 合法环境文件及稳定代次
- 组/其他用户可写文件拒绝与权限修复
- 文件路径和父目录符号链接
- `LD_PRELOAD`、`PATH`、`PYTHONPATH` 等危险变量（即使已列入白名单）
- 超长行和文件大小上限
- 危险内容重载失败时保留上一次完整可信快照
- inode 替换、哈希变化与代次递增
- 属主错误、非普通文件、语法错误、白名单外变量和重复变量

## 部署注意

生产环境应把 `trusted_owner` 设为 `0`，并让服务以 root 读取文件后再降权。
如果服务本身非 root，则只能信任该服务自己的 UID；不能把普通配置目录中的
任意文件当作特权配置。

组件保证不会接受攻击者写入的内容，但父目录可写仍允许攻击者通过替换文件造成
拒绝服务。部署时仍建议使用 root 持有的配置目录（如
`/etc/my-service`、`0755`）和 `service.env`、`0600`，并在收到 `SIGHUP`
或配置变更事件时调用 `load()` 完成完整重新校验。
