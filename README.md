# FastCGI 脚本路径拆分与上传目录门禁

PHP 站点常把上传的图片放在可访问目录里，攻击者上传内含脚本的 `avatar.jpg`，再请求
`/uploads/avatar.jpg/x.php`。若网关只按路径拆分脚本文件与附加路径，图片或上传文件
就可能被当作 PHP 脚本执行。

本包在虚拟站点根 `/` 上完成拆分，不访问真实文件系统：

- `Manifest` 的键是站点根相对路径；值非空表示符号链接目标。
- 脚本目录白名单和上传目录列表也使用站点根相对路径。
- 上传目录优先于脚本目录。请求路径或符号链接目标进入上传目录时，一律不产生脚本。
- 从左到右选择第一个 `.php` 段；后缀不区分大小写，段尾的点和空格会先移除。
- 脚本必须出现在清单中，解析完整符号链接链后仍是清单中的普通文件，并位于站点根内。
- 请求路径中的 NUL（包括 `%00`）和 `..` 段会被拒绝。

## 用法

```go
package main

import (
	"errors"
	"fmt"

	"example.org/netsec/q0777"
)

func main() {
	manifest := netsec.Manifest{
		"/scripts/index.php": {},
		"/uploads/avatar.jpg": {},
		// 指向上传目录的脚本链接必须被拒绝。
		"/scripts/avatar.php": netsec.Symlink("../uploads/avatar.jpg"),
	}

	result, err := netsec.SplitPath(
		"/scripts/index.php/images/1",
		manifest,
		[]string{"/scripts"},
		[]string{"/uploads"},
	)
	if err != nil {
		if errors.Is(err, netsec.ErrBlocked) {
			// 上传目录或目录白名单策略拒绝了该请求。
		}
		return
	}

	// ScriptFilename 是站点根相对路径；网关应前置真实站点根后写入
	// FastCGI 的 SCRIPT_FILENAME。
	fmt.Println(result.ScriptFilename) // /scripts/index.php
	fmt.Println(result.PathInfo)       // /images/1
}
```

`Splitter` 适合在启动时验证清单和目录配置，并在每个请求上并发复用：

```go
splitter, err := netsec.NewSplitter(manifest, []string{"/scripts"}, []string{"/uploads"})
if err != nil {
	// ErrInvalidManifest：清单或目录配置无法规范化为站点根内路径。
}

result, err := splitter.SplitPath("/uploads/shell.php/anything")
```

可能的错误：

- `ErrNotScript`：没有符合条件的脚本前缀，请求应作为静态资源或 404 处理。
- `ErrBlocked`：上传目录或目录白名单策略拒绝拆分。
- `ErrInvalidPath`：路径含 NUL、`..` 段、非法转义，或符号链接越出站点根/形成环。
- `ErrInvalidManifest`：清单或目录配置本身无效。
