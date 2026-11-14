# tar硬链接把解包写到宿主目录

固件构建服务解包上传的tar，攻击者放入指向`../../etc/passwd`的硬链接或先用符号链接铺垫再写入，归档成包时泄露宿主文件。请实现供Go构建服务调用的可导入安全解包组件。逐条目解析类型、名称和链接目标，所有路径按POSIX规则规范化后必须落在目标根内；硬链接目标必须是本次归档中已创建的普通文件且位于根内，符号链接不得作为后续写路径的祖先，设备、FIFO和`..`逃逸直接拒绝。稀疏文件、PAX扩展属性、重复条目和归档截断按声明一致性处理，失败时不发布任何部分结果。用硬链接逃逸、符号链接链、绝对路径、PAX路径覆盖、合法内部硬链接和深层目录验证宿主文件不可读写。

## `safeuntar` 组件

可导入包位于 `safeuntar`，构建服务只需调用一个入口：

```go
import "example.org/netsec/q0230/safeuntar"

// r 为上传 tar 的 io.Reader，dest 必须尚不存在。
if err := safeuntar.Extract(r, dest); err != nil {
    // 拒绝时 dest 不会被创建，已产生的暂存目录会被清理。
}
```

### 安全保证

- 条目名与硬链接目标按 POSIX 规则 `path.Clean` 规范化，绝对路径、任何 `..` 分量、NUL/反斜杠和超长分量直接拒绝（`ErrPathEscape`）。
- 硬链接只能指向本次归档中此前已创建的普通文件，且目标位于根内（`ErrHardLinkTarget`），无法指向 `/etc/passwd` 等宿主文件。
- 符号链接目标按其所在目录解析，绝对目标或逃逸根的相对目标拒绝（`ErrLinkEscape`）；任何后续条目的祖先若是符号链接也拒绝，杜绝“先铺符号链接再写入”。
- 字符/块设备、FIFO、contiguous 文件及未知类型一律拒绝（`ErrUnsupportedType`），仅放行普通文件、目录、硬链接和符号链接。
- PAX/GNU 长名、`path`/`linkpath` 覆盖在合并后重新走同一套路径校验。
- 稀疏文件按 PAX `GNU.sparse.*` 声明解析并校验区间有序、不重叠、不越界，写盘时保留空洞；截断、物理/逻辑字节不一致、`size`/`realsize` 冲突返回 `ErrMalformedArchive`。
- 重复条目（含与隐式父目录冲突）返回 `ErrDuplicateEntry`；空归档返回 `ErrEmptyArchive`；目标已存在返回 `ErrDestinationExists`。
- 解包先写入目标父目录下的隐藏暂存目录，成功后单次 `rename` 原子发布，任何错误都 `RemoveAll` 清理，不发布部分结果。

### 测试

```sh
go test ./...
go test ./safeuntar -race -cover
```

测试用手工构造的 512 字节 tar 块覆盖硬链接逃逸、符号链接链、绝对路径、PAX 路径/链接覆盖、合法内部硬链接、深层目录、设备/FIFO、重复条目、截断与稀疏一致性等场景，并断言失败后目标路径与暂存目录均不存在、宿主哨兵文件内容不变。
