# cgroup 逃逸信号检测

Go 宿主侧 cgroup 逃逸检测组件：订阅主机审计事件流，关联 `mount`、
`openat`、`write` 与 cgroup 迁移事件，识别被入侵沙箱任务的逃逸手法，
命中后调用调用方注册的阻断回调并附带完整事件链。

## 检测规则

- `release_agent_write`：向 cgroup v1 `release_agent` 文件写入（常与
  `notify_on_release` 组合实现宿主侧命令执行）。
- `notify_on_release_open`：以写方式打开 `notify_on_release`。
- `writable_cgroup_mount`：沙箱内挂载可写的 `cgroup`/`cgroup2` 文件系统；
  只读挂载与非 cgroup 文件系统不告警。
- `migrate_to_parent`：通过 `cgroup.procs`/`cgroup.threads` 把任务迁入
  沙箱 cgroup 根之外（含上级）。

## 命名空间与合法操作判定

- 优先用审计事件的 cgroup namespace inode 归属沙箱，缺失时按宿主 cgroup
  路径最长前缀兜底，避免兄弟沙箱互相误判。
- 宿主机初始 cgroup namespace 内、通过 `AllowManager` 登记的合法管理器
  （如 `systemd`、`kubelet`、`containerd`）执行的挂载与 cgroup v2 子树
  管理一律放行。
- 沙箱内部向自身 cgroup 子树写 `cgroup.procs`（正常调度/子树管理）放行。
- 对 `notify_on_release` 的只读访问、普通业务文件写入不告警。

## 事件链

检测器为每个沙箱维护带时间窗口（默认 30s）与长度上限（默认 64 条）的
近期事件缓冲。命中时按时间与审计序号排序，输出包含触发事件在内的
`Alert.Chain`，供阻断回调做 kill、隔离或取证。

## 用法

```go
source := netsec.NewChannelSource(128) // 也可包装 auditd / eBPF 适配器

detector := netsec.NewDetector(netsec.Config{
    Sandboxes: []netsec.SandboxConfig{{
        ID:             "sandbox-1",
        CgroupNSInode:  4026532345,
        HostCgroupRoot: "/sys/fs/cgroup/kubepods/podabc/sandbox-1",
    }},
    Block: func(alert netsec.Alert) {
        // 阻断：kill 任务、撤销凭据等；alert.Chain 为关联事件链
    },
})
detector.AllowManager(4026531835, "systemd") // 初始 namespace 的合法管理器

go func() { _ = detector.Run(ctx, source) }()
```

## 测试

`detector_test.go` 覆盖 release_agent 写入、可写 cgroup 挂载、迁入上级、
正常调度放行、命名空间边界与通道订阅链路：

```sh
go test ./...
```

沙箱任务被入侵后，攻击者会写 release_agent、打开 notify_on_release，或把自己迁入上级 cgroup 来逃出资源与进程限制。为 Go 宿主监控写可订阅主机审计事件流的 cgroup 逃逸检测组件，关联 mount、openat、write 和 cgroup.procs 迁移事件，区分沙箱 cgroup 命名空间内外与合法 cgroup v2 子树管理；命中后调用调用方的阻断回调并附事件链。用 release_agent 写入、可写 cgroup 挂载、迁入上级及正常调度事件验证。
