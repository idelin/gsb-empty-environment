# pathsplit

`pathsplit` 为 Go FastCGI 网关拆分 PHP `SCRIPT_FILENAME` 与 `PATH_INFO`。
它基于站点根目录的虚拟文件清单工作，不会访问宿主文件系统。

```go
import "prescreen.local/ps-847c68412f/pathsplit"

s, err := pathsplit.New(pathsplit.Config{
	Root: "/srv/www",
	Files: pathsplit.Manifest{
		"/app":               {Kind: pathsplit.Directory},
		"/app/index.php":     {Kind: pathsplit.RegularFile},
		"/uploads":           {Kind: pathsplit.Directory},
		"/uploads/shell.php": {Kind: pathsplit.RegularFile},
	},
	ScriptDirs: []string{"/app"},
	UploadDirs: []string{"/uploads"},
})
if err != nil {
	// invalid configuration
}

result, err := s.Split("/app/index.php/users/42")
// result.ScriptFilename == "/srv/www/app/index.php"
// result.PathInfo == "/users/42"
```

清单路径、脚本目录和上传目录都使用站点根相对路径，前导 `/` 可省略。
未在清单中声明的中间目录视为隐式目录；最终脚本必须显式存在于清单中。
符号链接使用 `{Kind: pathsplit.Symlink, Target: "..."}` 表示。根相对目标以
`/` 开头，其他目标相对链接所在目录解析。

拆分时会：

- 对 URL 路径做百分号解码，并拒绝 NUL、反斜杠、非法转义和 `..` 段。
- 以不区分大小写的方式识别 `.php`，先去掉后缀后的点与空格。
- 从前往后选择第一个存在、位于 `ScriptDirs` 物理目录内、不在
  `UploadDirs` 内，且不是符号链接环的普通文件。
- 递归展开中间和末尾符号链接，拒绝逃出站点根或落到上传目录的路径。
- 任何位于上传目录下的请求路径都直接拒绝，不会执行其中的 PHP 文件。

失败时返回可与 `errors.Is` 匹配的错误，例如 `ErrUploadPath`、
`ErrSymlinkLoop`、`ErrUnsafePath`、`ErrOutsideRoot` 和
`ErrScriptNotAllowed`。

运行测试：

```sh
go test ./...
```
