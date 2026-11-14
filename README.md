# 拖放文件落到了可写目录之外

远程桌面拖放按路径接收文件，攻击者构造绝对路径或符号链接，把文件写到特权目录。请实现供Go远程桌面服务调用的可导入拖放目标组件。目标目录由会话策略固定，相对路径规范化后必须落在目录内，符号链接解析后复核；可执行类型与大小受限。覆盖与重命名使用原子替换。用绝对路径、符号链接、目录穿越、合法拖放和覆盖验证只写入目标目录。

## 组件

`netsec` 包提供可直接导入的拖放目标组件，入口类型为 `Target`，由会话策略 `Policy` 构造：

```go
target, err := netsec.NewTarget(netsec.Policy{
    Root:        "/srv/session/drop", // 会话固定目标目录（必须绝对路径）
    MaxFileSize: 10 << 20,            // 单文件上限 10MiB
    // DeniedExtensions 留空使用默认可执行黑名单；传 []string{} 关闭扩展名拦截
})
if err != nil { /* 根目录不存在/为逃逸链接等 */ }

// 接收拖放文件，relPath 为客户端给出的相对路径
rel, err := target.ReceiveFile(ctx, "report/q3.txt", reader)

// 会话内原子重命名/整理
err = target.Rename("report/q3.txt", "archive/q3.txt")
```

## 安全保证

- **固定根目录**：`NewTarget` 即对根目录做 `EvalSymlinks`，根为逃逸符号链接或非目录时直接构造失败。
- **路径规范化**：拒绝空路径、NUL 字节、绝对路径、Windows 卷/UNC 路径与任何 `..` 逃逸；词法清洗后逐段映射。
- **符号链接复核**：中间目录与末级文件上的符号链接逐段 `EvalSymlinks`，解析结果不在根内即返回 `ErrOutsideRoot`；写入和重命名完成后再次复核最终真实路径。
- **类型与大小限制**：默认拒绝 `.exe/.dll/.so/.dylib/.sh` 等可执行扩展名（大小写不敏感，可由策略覆盖）；读取阶段即按 `MaxFileSize` 截断，超限返回 `ErrTooLarge`，拒绝空文件。
- **原子替换**：内容先写入根内 `.drop-staging` 临时文件（0600、非可执行），`Sync` 后用 `rename(2)` 原子覆盖或重命名；失败清理临时文件，覆盖符号链接时替换链接本身而非跟随写入。
- **错误判定**：安全错误均为哨兵错误（`ErrInvalidPath`、`ErrOutsideRoot`、`ErrExecutableDenied`、`ErrTooLarge`、`ErrEmptyContent`），用 `errors.Is` 判定。

## 验证

```sh
go test -race -count=1 ./...
```

测试覆盖绝对/卷路径、目录穿越、符号链接逃逸（目录链接、文件链接、组合穿越）、根内合法链接、可执行与大小限制、合法拖放、原子覆盖与重命名，并对根外目录做前后快照对比，证明任何被拒请求都不会在目标目录之外产生写入。
