# 挂载传播逃逸判定

沙箱控制面要判断新挂载是否会传播到宿主。写供Go启动器调用的挂载图包，节点标记私有、共享组或从属组，挂载事件沿共享同伴及下游从属传播，禁止反向传播；移动挂载要保持父子关系一致。返回所有受影响命名空间及传播路径，用有限图重放验证循环组、断开传播和禁止宿主可见的更新。

## 用法

```go
g := netsec.NewGraph("host-ns")

// 宿主共享组，沙箱组作为其下游从属组。
_ = g.AddGroup("g-host", netsec.Shared, "")
_ = g.AddGroup("g-sandbox", netsec.Slave, "g-host")

_ = g.AddNode(netsec.Node{ID: "host-root", Namespace: "host-ns", Type: netsec.Shared, Group: "g-host"})
_ = g.AddNode(netsec.Node{ID: "box-root", Namespace: "sandbox", Type: netsec.Slave, Group: "g-sandbox"})

// 宿主发起的挂载会沿下游从属进入沙箱。
report := g.Propagate("host-root")
report.HostVisible   // true
report.Namespaces    // [host-ns sandbox]

// 沙箱内的从属事件不能反向回到宿主。
g.HostReachable("box-root") // false

// 重放一批更新，宿主禁用策略下任何宿主可见的挂载/移动都会被拒绝并回滚。
res := netsec.Replay(events, netsec.ReplayOptions{
    HostNamespace:     "host-ns",
    ForbidHostVisible: true,
})
res.HostBlocked // 被 ErrHostForbidden 拒绝的事件下标
```

## 设计要点

- 传播方向：共享组同伴之间双向；主组到下游从属组单向，从属组永不回灌主组。
- 环检测：从属组建边或 `SetMaster` 重绑时做 DAG 校验，成环返回 `ErrCycle`。
- 移动一致性：`Move` 拒绝根挂载、跨命名空间目标和移入自身后代，父子边原子更新。
- 有限重放：`Replay` 逐事件落地，失败不改图；移动用快照预演，宿主可见则整体回滚。
- 传播路径：每个受影响节点保留 BFS 最短 `Hop` 序列，机制区分为共享同伴与下游从属。
