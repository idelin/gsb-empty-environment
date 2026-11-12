# Landlock叠加不降级

进程要叠加多层 Landlock 规则收紧文件访问。内核 ABI 较旧时，请求中的新访问权限若被静默丢弃，这些权限反而不受限制。为 Go 沙箱启动器写可导入的规则组合包，输入目标 ABI、各层 handled 权限和路径规则，计算叠加后某路径的有效权限，父目录规则与子目录规则按 Landlock 语义相交；旧 ABI 不支持被请求处理的权限时拒绝而不是降级。用 ABI 1 请求 REFER 与 TRUNCATE、父子目录叠加和合法收紧验证。

## netsec 包

`netsec` 是纯 Go 的 Landlock 文件系统规则组合器，不发起系统调用，只对目标内核
ABI 做建模与校验：

- `ABI.SupportedAccessFS()` 给出该 ABI 能表示的全部 `AccessFS` 权限位（ABI 1–6，
  数值与 `include/uapi/linux/landlock.h` 一致）。
- `Compose(abi, layers)` 校验并编译规则栈。任一层的 `Handled` 或路径规则包含
  目标 ABI 不支持的权限位时返回 `*UnsupportedAccessError`，绝不静默丢弃。
- `Profile.EffectiveAccess(target)` 计算某路径的有效权限：单层内所有命中的规则
  （含祖先目录规则）取并集，跨层取交集；某层 handled 但无命中规则即拒绝。
- `Effective` 同时返回 `Handled`、`Allowed`、`Denied` 和 `Ungoverned`，未被任何
  层处理的权限明确标记为不受治理（即不受限制），避免误判为已拒绝。
- 处理 `REFER` 的规则集必须同时处理 `REMOVE_DIR|REMOVE_FILE|MAKE_DIR|MAKE_REG`，
  与内核 `landlock_create_ruleset(2)` 的 EINVAL 校验保持一致。

```go
profile, err := netsec.Compose(netsec.ABI1, []netsec.Layer{
    {
        Handled: netsec.AccessFSReadFile | netsec.AccessFSWriteFile,
        Rules: []netsec.PathRule{
            {Path: "/srv", Dir: true, Allowed: netsec.AccessFSReadFile},
            {Path: "/srv/out", Dir: true, Allowed: netsec.AccessFSWriteFile},
        },
    },
})
if err != nil {
    // ABI 1 不支持的权限（如 REFER、TRUNCATE）会在这里失败关闭。
    return err
}
eff, err := profile.EffectiveAccess("/srv/out/dump.bin")
// eff.Allowed == READ_FILE|WRITE_FILE
```

## 验证

- ABI 1 请求 `REFER`（ABI 2）与 `TRUNCATE`（ABI 3）均返回
  `*UnsupportedAccessError`，并精确报告不支持的位。
- 父子目录叠加：层内并集、跨层交集，无命中规则的 governed 权限被拒绝。
- 合法收紧：逐层叠加后有效权限单调不增。

运行 `go test ./...` 执行全部用例。
