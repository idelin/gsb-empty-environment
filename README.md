# MSI 中断重映射：requesterID + deviceGeneration 限定路由

直通设备可写 MSI-X 表。如果中断重映射项只按 vector 查找，租户可以把中断投递到宿主或另一虚拟机的 vCPU。本仓库为 Go 虚拟化宿主提供可导入的 `netsec` 中断重映射包，映射键始终是 `(requesterID, deviceGeneration, tableIndex)`，vector 只用于命中后的消息一致性校验，不能单独选路。

## 主要 API

- `NewRemapper()` 创建宿主侧重映射器。
- `RegisterDomain(id, DomainConfig)` 登记 vCPU 集合、vCPU 代次和允许的 vector 范围。
- `NewDomain(id, DomainConfig)` 登记 domain 并返回宿主签发的 `*Domain` capability；`Device.InstallAs` / `InstallBatchAs` 会校验该 capability，不能用一个 domain 的句柄去编程另一个 domain 的设备。
- `AttachDevice(requesterID, owner)` 由宿主把不可伪造的 requesterID 绑定到所属 domain，返回租户可见的 `*Device`。
- `Device.Install(owner, InstallRequest)` 安装/替换单条表项，执行 close → drain in-flight receipts → publish。
- `Device.InstallBatch` / `InstallBatchContext` 批量失败关闭更新。
- `Device.Reset` 和 `Remapper.Reassign` 递增设备代次、撤销旧代次全部表项并对旧在途回执做失效屏障。`Reassign` 返回新 owner 的新 `*Device` 句柄并吊销旧句柄，旧租户之后无法继续投递或 Reset。
- `Device.SendInterrupt(index, vector, callback)` 模拟设备只伪造 index/vector 的 MSI-X 写入；requesterID 和当前代次由宿主句柄盖章。
- `Remapper.Deliver(Interrupt, callback)` 是可信根复合体/IOMMU 入站路径；绝不能暴露给租户。

## 安全不变量

- 安装请求必须来自设备所属 domain；vCPU 必须在集合中且代次为当前值；vector 必须在 domain 范围内；deliveryMode 只允许 `Fixed`。
- 中断到达先按 requesterID、设备代次、tableIndex 匹配表项，再要求消息 vector 等于该表项登记 vector。
- 投递回调前在同一临界区重验 domain 存在、vCPU 属于集合、vCPU 代次当前、vector 仍在范围内，并再次确认表项目标 domain 就是设备当前 owner。
- 更新先置 `present=false`，等待该表项所有回调回执返回，再写新目标并发布 `present=true`。等待期间新中断看到关闭表项。
- 设备 Reset/转租先递增代次，因此携带旧代次的请求立即失配；旧表项随后撤回并等待在途回执。
- 转租完成后旧 `*Device` 句柄失效（`ErrStaleDeviceHandle`），避免前租户继续控制已转租设备。
- 批量更新先把所有目标表项置为不可投递，全部验证通过后才逐项做失效屏障和发布。任一项失败时不回滚已经关闭/已经安全发布的项；失败项保持关闭。

## 测试与攻击轨迹

```sh
go test ./...
go test -race ./...
go vet ./...
```

测试覆盖伪造 vector、旧代次请求、Reset/转租与在途投递竞态、vCPU 热移除、批量部分失败和并发安装/投递。`TestShortestCrossDomainTraceSearch` 对真实实现做有界动作序列搜索：安全实现深度 4 内无“非所属 domain 收到中断”的轨迹；同一搜索器对“只按 vector 查找”的参考模型给出最短 2 步轨迹：

```text
install-b-vector-40 -> send-a-vector-40
```

即设备 A 发送设备 B 已登记的 vector 时，向量单键实现会把中断投到 B；本实现因 requesterID/代次/索引限定而在投递前返回 `ErrVectorMismatch`。

## 信任边界

`RequesterID` 由宿主/根复合体分配，`Device.SendInterrupt` 不接受调用方传入 requesterID。租户只持有 `*Device`，不得持有 `*Remapper`，否则可直接调用宿主管理接口和原始入站接口。回调在无内部锁状态执行，但不得回调同一重映射器的控制操作，否则会与等待其回执的失效屏障互相等待。
