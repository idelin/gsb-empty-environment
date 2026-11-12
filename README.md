# Postgres扩展安装闭包

低权限数据库用户可能借 `CREATE EXTENSION` 的依赖链引入未受信扩展，或用路径技巧加载任意共享库。本包为 Go 数据库代理提供可导入的准入判断，输入控制文件元数据、请求角色与已安装扩展目录。

**准入规则**

- 非超级用户只能安装 `trusted = true` 的扩展。
- `requires` 的传递闭包必须全部已安装且 `trusted = true`，循环依赖可安全终止。
- `superuser = true` 或 `superuser_only = true` 的扩展拒绝非超级用户。
- 超级用户可以安装任意扩展，但共享库路径仍走统一白名单。
- `LOAD`、C 函数库名和 `module_pathname` 只接受 `$libdir/<basename>`；拒绝绝对路径、嵌套路径、`..`、反斜杠和 Windows 盘符。

**基本用法**

```go
ext := netsec.Extension{
    Name:     "hstore",
    Trusted:  true,
    Requires: []string{"plpgsql"},
}

installed := netsec.InstalledExtensions{
    "plpgsql": {Name: "plpgsql", Trusted: true},
}

err := netsec.EvaluateInstall(netsec.Role{Name: "alice"}, ext, installed)
if err != nil {
    // 拒绝安装
}
```

便捷入口 `CheckExtensionInstall`、`AdmitInstall`、`CheckInstall` 接受 `Role`、`bool`（true 表示超级用户）或角色名作为请求角色，也接受 `map[string]Extension`、`map[string]bool` 等已安装目录形式。错误可用 `errors.Is` 对 `ErrExtensionNotTrusted`、`ErrSuperuserRequired`、`ErrDependencyNotInstalled`、`ErrDependencyNotTrusted`、`ErrInvalidLibraryPath` 等哨兵值判断。

**库路径**

```go
if err := netsec.ValidateLibraryPath("$libdir/libhstore.so"); err != nil {
    // 拒绝 LOAD / C 函数
}
```

控制文件文本可用 `ParseControlFile` 解析；`superuser` 缺省值遵循 PostgreSQL 语义，默认为 `true`。
