# NVMe管理操作码门控

直通给不可信租户的 NVMe 控制器，其管理命令可格式化、固件下载或操作其他命名空间。为C++17虚拟 NVMe 设备写供管理队列分发调用的操作码门控库：只放行 Identify、受限的 Get Log Page（日志页白名单）、Get Features 与 Create/Delete I/O 队列；Format NVM、Sanitize、Firmware Commit 与 Image Download、Security Send 与 Receive、Namespace Management 及所有厂商自定义管理操作码一律拒绝；NSID 为 0xFFFFFFFF 时只对租户命名空间集合展开，不得影响其他命名空间。用逐个操作码、广播 NSID、厂商码 0xC0 和不在白名单的日志页验证。
