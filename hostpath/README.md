# hostpath 宿主路径判定包

供准入控制器导入的纯判定逻辑，不访问真实文件系统；宿主符号链接由调用方以
`SymlinkTable` 显式提供，构造后的 `*Validator` 只读、可并发复用。

## 判定规则

- 路径必须为绝对路径；先去重复斜杠与 `.` 段得到折叠前结果，再对 `..` 做词法
  折叠得到折叠后结果，两者都必须落在白名单前缀内。
- 白名单按路径段边界比较，`/var/log` 不覆盖 `/var/log2`。
- 符号链接逐段展开（支持绝对/相对目标与环检测），展开结果仍须在白名单内。
- 折叠或展开后命中禁止清单（`/`、`/proc`、`/sys`、`/etc`、
  `/var/lib/kubelet`、基名 `*.sock`）一律拒绝，即使位于白名单前缀之下。
- `type` 为空时不做存在性检查，仍按路径判定；非法 type 返回 `ErrUnknownType`。
- `subPath`/`subPathExpr` 含 `..` 段、绝对路径或展开后越出卷目录一律拒绝；
  `subPathExpr` 按 `$(VAR_NAME)` 展开，未定义变量返回 `ErrSubPathVar`。
- `ReadOnly == false` 的挂载，卷路径与实际挂载点都必须落在标注可写的前缀之下。

## 用法

```go
v := hostpath.New(
    []hostpath.Prefix{
        {Path: "/var/log", Writable: false},
        {Path: "/srv/data", Writable: true},
    },
    nil, // nil 表示使用 DefaultDenyList
    hostpath.SymlinkTable{"/var/log/rootlink": "/"},
)

// 仅判定卷路径
err := v.ValidateHostPath(hostpath.HostPathVolume{
    Path: "/var/log/../run/containerd",
})

// 判定容器挂载（含 subPath/subPathExpr 与读写约束）
err = v.ValidateMount(
    hostpath.HostPathVolume{Path: "/srv/data", Type: "Directory"},
    hostpath.VolumeMount{SubPathExpr: "$(POD_NAME)/conf", ReadOnly: false},
    map[string]string{"POD_NAME": "pod-1"},
)
```

失败原因通过哨兵错误暴露，可用 `errors.Is` 判别：`ErrNotAbsolute`、
`ErrWhitelist`、`ErrDenied`、`ErrSymlinkLoop`、`ErrUnknownType`、
`ErrSubPathAbsolute`、`ErrSubPathParent`、`ErrSubPathEscape`、
`ErrSubPathVar`、`ErrSubPathConflict`、`ErrWritable`。

运行测试：`go test ./...`
