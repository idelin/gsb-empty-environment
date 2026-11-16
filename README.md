# Git 仓库属主与 safe.directory 判定

这个包用于 Go Git 封装库在读取仓库本地配置前做信任判定。典型场景是共享目录中预先放置了带恶意配置或钩子的 `.git` 目录：调用方必须先确认仓库可信，再读取或执行任何依赖它配置的操作。

## 安全规则

- 工作树和 Git 目录都必须属于当前用户，除非系统或全局配置中的 `safe.directory` 明确允许。
- `safe.directory` 支持精确路径、单独的 `*`，以及以 `/*` 结尾的前缀；前缀不会匹配基目录本身或字符串相似的兄弟目录。
- 仓库本地 `.git/config` 中的 `safe.directory` 永远不参与授权，即使通过 `GIT_CONFIG_GLOBAL` 指向该文件也会被排除。
- 如果存在 `SUDO_UID`，所有权比较使用该 UID，而不是 sudo 后的有效 UID；非法 `SUDO_UID` 会失败关闭。
- 向上发现 `.git` 时，遇到他人属主且未被保护配置允许的目录立即停止，不会越过属主边界读取上层 `.git`。
- 保护配置只读取 Git 的系统级和全局级位置，不解析 `include.*`，避免被仓库内容间接影响。

## 使用

```go
import netsec "example.org/netsec/q0826"

repo, err := netsec.Discover(".")
if err != nil {
	// 发现失败，或发现过程遇到不可信任的上级目录。
	return err
}
if !repo.Trusted {
	// 不要读取 repo.ConfigPath()。
	return netsec.ErrUntrustedRepository
}

data, err := repo.ReadConfig()
```

最简形式会重新执行发现和信任判定，再读取本地配置：

```go
data, err := netsec.ReadConfig(".")
```

还可以直接检查一个仓库根目录：

```go
repo, err := netsec.Inspect("/path/to/repo")
if err == nil && repo.Trusted {
	// repo.Reason 为 "owner" 或 "safe-directory"。
}
```

`Checker` 和 `With*` 选项用于测试或自定义环境，例如注入属主查询、固定当前 UID、替换保护配置路径。默认实现使用进程有效 UID、Unix 文件属主和 Git 的标准配置位置（`GIT_CONFIG_SYSTEM`、`GIT_CONFIG_GLOBAL`、`GIT_CONFIG_NOSYSTEM`、`XDG_CONFIG_HOME`、`$HOME/.gitconfig`）。

`ReadConfig` 只在 Unix 类系统上可完成属主判定；其他平台会明确返回不支持错误，而不是默认放行。
