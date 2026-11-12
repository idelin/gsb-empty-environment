# VFIO组完整性

VFIO 要求同一 IOMMU 组的设备同属一个用户。若组内还有设备绑定在宿主驱动上，
租户就能借点对点 DMA（P2P DMA）触及它，因此直通代理在把组挂入 VFIO 容器前
必须验证整组完整性，并在虚拟机运行期间持续守护。

本包 `netsec` 为 Go 直通代理提供“虚拟机启动时调用”的 IOMMU 组完整性检查与
运行时撤销能力。

## 安全策略

- **整组绑定校验**：只有组内所有设备都绑定到 VFIO 驱动（`vfio-pci`）或桩驱动
  （`pci-stub`、`pciback` 等）时，组才可用。仍挂在宿主驱动（如 `nvme`、`ixgbe`）
  或完全未绑定驱动的设备会让整个组被拒绝，错误为 `*IntegrityError`。
- **一组一容器**：一个 IOMMU 组同一时刻只存在一个租约（`Lease`），只能挂入一个
  容器；同租户重复请求返回同一租约（幂等），不会产生第二份挂载。
- **跨租户一律拒绝**：组已属租户 A 时，租户 B 的请求返回 `ErrTenantConflict`。
  只有在原租约被 `Release` 或被撤销后，其他租户才能取得该组。
- **运行时立即撤销**：守护期间一旦出现下列变化，立即撤销该组（撤销租约、停止
  监听、从注册表摘除），并通过 `RevokeHook` 通知代理把组从容器摘除并隔离租户：
  - 新成员热插入（`RevokeHotplug`，即使新成员已在 vfio 驱动上也撤销）；
  - 成员设备热拔出（`RevokeHotRemove`）；
  - 成员驱动被重新绑定，包括解绑、绑到宿主驱动、在两个允许驱动之间切换
    （`RevokeRebind`）；
  - 复检发现完整性被破坏（`RevokeIntegrity`）或整个组从 sysfs 消失
    （`RevokeGroupGone`）。

## 组件

- `sysfs.go`：`Sysfs` 抽象与生产实现 `LinuxSysfs`，按真实 Linux 布局解析
  `/sys/kernel/iommu_groups/<gid>/devices/*` 符号链接及每个设备的 `driver` 链接。
- `group.go`：纯策略 `CheckGroup`，以及 `IntegrityError`、`DeviceViolation` 与
  允许驱动集合（可用 `WithAllowedDrivers` 扩展）。
- `watcher.go`：`Watcher` 接口与可移植的轮询实现 `PollingWatcher`。它快照目录项
  及其符号链接目标，因此能识别“同名符号链接被原子改指”这种重新绑定。
- `watcher_inotify_linux.go` / `watcher_default_*.go`：Linux 上默认使用 inotify
  即时事件；其他平台（以及测试）回退到轮询。
- `manager.go` / `manager_watch.go`：`Manager` 负责租户归属、单租约约束、
  附加时双重检查（消除检查与挂载之间的 TOCTOU 窗口）以及运行时守护与撤销。

## 典型用法

```go
mgr := NewManager(NewLinuxSysfs(),
    WithRevokeHook(func(r Revocation) {
        // 把该组从 VFIO 容器摘除并隔离 r.TenantID
    }),
)
defer mgr.Close()

// 虚拟机启动前：校验并挂载到该租户的容器
lease, err := mgr.Attach(groupID, "tenant-a")
if err != nil {
    return err // *IntegrityError / ErrTenantConflict / ...
}

select {
case rev := <-lease.Revoked():
    // 运行中完整性被破坏，rev.Reason 说明原因
}
```

## 测试

测试用临时目录构造仿真 sysfs（`sysfs_fake_test.go`），并走生产 `LinuxSysfs`
解析路径，因此可在非 Linux 开发机上完整验证。运行：

```sh
go test -race -count=1 ./...
GOOS=linux GOARCH=amd64 go build ./...   # 验证 inotify 后端交叉编译
```

覆盖的关键场景：

- 组内存在宿主驱动 / 未绑定设备 → 校验与挂载均被拒绝；
- 同一组分给两个租户 → `ErrTenantConflict`，且不产生第二份挂载；
- 运行中热插入新成员 → 立即 `RevokeHotplug` 并摘除；
- 运行中重新绑定（宿主驱动、解绑、允许驱动间切换）→ 立即 `RevokeRebind`；
- 另含热拔出、整组消失、撤销回调、`Close` 与纯策略单测。
