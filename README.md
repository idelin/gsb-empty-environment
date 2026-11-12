# NVMe管理操作码门控

直通给不可信租户的 NVMe 控制器，其管理命令可格式化、固件下载或操作其他命名空间。为C++17虚拟 NVMe 设备写供管理队列分发调用的操作码门控库：只放行 Identify、受限的 Get Log Page（日志页白名单）、Get Features 与 Create/Delete I/O 队列；Format NVM、Sanitize、Firmware Commit 与 Image Download、Security Send 与 Receive、Namespace Management 及所有厂商自定义管理操作码一律拒绝；NSID 为 0xFFFFFFFF 时只对租户命名空间集合展开，不得影响其他命名空间。用逐个操作码、广播 NSID、厂商码 0xC0 和不在白名单的日志页验证。

## 策略

- 操作码默认拒绝，只接受 `Identify (0x06)`、`Get Log Page (0x02)`、`Get Features (0x0A)`、`Create/Delete I/O Submission Queue (0x01/0x00)` 与 `Create/Delete I/O Completion Queue (0x05/0x04)`。
- `Get Log Page` 默认只允许 `0x00`、`0x01`、`0x02`、`0x03`、`0x05`；可通过构造函数提供自定义白名单。
- 所有 `0xC0` 至 `0xFF` 的管理操作码均按厂商自定义操作码拒绝。
- 具体 NSID 必须属于构造时登记的租户命名空间集合；NSID `0` 表示控制器作用域。
- `NSID == 0xFFFFFFFF` 时，`GateDecision::target_namespaces` 只包含去重并排序后的租户命名空间。拒绝的请求不返回任何目标。

## 使用

```cpp
netsec::AdminQueueGate gate({2, 5, 9});

netsec::AdminCommandRequest cmd;
cmd.opcode = netsec::admin_opcode::kIdentify;
cmd.nsid = netsec::kBroadcastNsid;

const auto decision = gate.evaluate(cmd);
if (decision) {
    // 只对 decision.target_namespaces 分发，不接触其他 NSID。
}
```

## 构建与验证

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```
