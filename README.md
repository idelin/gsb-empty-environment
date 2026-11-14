# 模块根路径安全组件

`example.org/netsec/q0353` 提供一个可导入的 Go 组件，供文件同步服务把请求路径限制在按模块名配置的目录内。模块根只来自可信配置；请求方不能提交或替换根路径。

## 快速使用

```go
import (
	"io/fs"

	netsec "example.org/netsec/q0353"
)

modules, err := netsec.NewModules(map[string]string{
	"alpha": "/srv/sync/alpha",
	"beta":  "/srv/sync/beta",
})
if err != nil {
	return err
}
defer modules.Close()

data, err := modules.ReadFile("alpha", "docs/readme.txt")
if err != nil {
	return err
}
err = modules.WriteFile("alpha", "docs/readme.txt", data, fs.FileMode(0o640))
```

也可以直接操作单个模块根：

```go
root, err := netsec.NewRoot("/srv/sync/alpha")
if err != nil {
	return err
}
defer root.Close()

if err := root.WriteFile("docs/readme.txt", data, 0o640); err != nil {
	return err
}
```

## 安全边界

- **根只来自配置**：`NewRoot` / `NewModules` 在构造时把配置路径变为绝对路径并解析符号链接，之后请求 API 只接受模块相对路径。
- **词法规范化**：拒绝空路径、NUL、反斜杠、Unix 绝对路径、Windows 盘符/绝对形式；若百分号编码在最多多轮解码后呈绝对路径、控制字符或越根 `..` 也被拒绝。`path.Clean` 之后仍位于根外的 `..` 被拒绝；`a/../b` 这类规范化后仍在根内的路径允许。
- **符号链接二次核对**：每次操作先检查请求路径，再用 `filepath.EvalSymlinks` 解析并核对解析结果仍在根内。读取可跟随指向根内的相对符号链接；写入拒绝最终组件是符号链接，避免静默替换链接或写到不确定的目标。
- **内核级根约束**：文件打开、stat、mkdir、remove 使用 Go 1.24 的 `os.Root`，路径逃逸由 `openat` 层再次阻止。
- **原子写**：在目标同目录创建 `O_EXCL` 临时文件，写入并 `fsync`，然后通过已固定的父目录句柄 `renameat` 替换目标。替换前后分别核对临时文件和目标文件的 inode；失败路径清理临时文件。
- **受限重命名/删除**：源/目标父目录先打开为目录句柄，重命名通过 `renameat` 执行，目标 inode 必须等于源 inode。删除只使用父目录句柄，删除后原 inode 不得仍位于请求路径。
- **模块隔离**：模块名是配置 map 的精确键，不参与路径拼接。`NewModules` 拒绝相同、互为父子的模块根，防止一个模块的根包含另一个模块。

操作失败通常会包装以下哨兵错误，可用 `errors.Is` 判断：

- `ErrInvalidPath`
- `ErrPathEscape`
- `ErrSymlink`
- `ErrInodeMismatch`
- `ErrUnknownModule`
- `ErrUnsafeWrite`

`OpenFile` 只允许只读标志；写操作必须走 `WriteFile`、`WriteFrom` 或 `WriteFunc`，防止调用方拿到可写文件描述符绕过原子替换。

## API

`Root`：

- `Resolve` / `ResolvePath` / `SafeJoin`：返回解析后的绝对路径，仅用于展示；I/O 应继续使用 `Root` 方法。
- `Open`、`OpenFile`、`ReadFile`、`Stat`、`Lstat`
- `Mkdir`、`MkdirAll`
- `WriteFile`、`WriteFrom`、`WriteFunc`、`AtomicWrite`
- `Rename`、`Remove`、`RemoveAll`
- `Validate`、`Relative`、`Name`、`Path`、`Close`

`Modules` 提供同名模块级转发：`ReadFile`、`Open`、`Stat`、`WriteFile`、`WriteFrom`、`Mkdir`、`MkdirAll`、`Rename`、`Remove`、`RemoveAll`、`Resolve`。

## 验证

测试覆盖路径穿越、绝对路径、外部与相对符号链接、链接链、合法同步生命周期、原子替换 inode、并发写、递归删除和模块隔离：

```sh
go test ./...
go test -race ./...
go vet ./...
```

## 部署注意

- 模块根目录必须由可信配置提供，且模块之间不能重叠。不要允许用户上传内容直接落入模块根之外的挂载点或设备文件。
- `os.Root` 和普通路径检查不会识别绑定挂载、特殊设备或跨边界硬链接别名。模块根应放在受控存储上，避免不可信进程能预先创建这些文件系统对象。
- Darwin/Linux 使用 `renameat`；其他平台回退为绝对路径重命名，但仍保留父目录 inode 复核。目标平台需要支持 Go 1.24 的 `os.Root`。
