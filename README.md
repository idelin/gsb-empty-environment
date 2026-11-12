# cgroup逃逸信号检测

沙箱任务被入侵后，攻击者会写 release_agent、打开 notify_on_release，或把自己迁入上级 cgroup 来逃出资源与进程限制。为 Go 宿主监控写可订阅主机审计事件流的 cgroup 逃逸检测组件，关联 mount、openat、write 和 cgroup.procs 迁移事件，区分沙箱 cgroup 命名空间内外与合法 cgroup v2 子树管理；命中后调用调用方的阻断回调并附事件链。用 release_agent 写入、可写 cgroup 挂载、迁入上级及正常调度事件验证。
