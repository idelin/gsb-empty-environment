# 智能卡PIN计数断电安全

智能卡校验 PIN 时，攻击者可在比较之后、计数器写回之前拔卡或断电，白拿无限次尝试。为Go读卡器仿真宿主写供 APDU 分发调用的 PIN 校验处理器：重试计数先扣减并落盘，再比较，成功后才恢复到上限；落盘用可注入断电的非易失存储，每个写入点都能被打断；计数为 0 后拒绝所有校验，只有 PUK 重置能恢复且 PUK 自身也有计数。用在每个断电点重启后总尝试数不超过上限、成功恢复、PUK 耗尽和并发两次校验验证。

## 接口

- `NewPINHandler(nvm SlotNVM, cfg Config)`：打开或首次初始化卡片。
- `VerifyPIN` / `VerifyPINBytes`：校验 PIN，失败返回 `0x63Cx`（x 为剩余次数），阻断返回 `0x6983`。
- `ResetPIN` / `ResetPINBytes`：PUK 校验并设置新 PIN，成功后同时恢复 PIN 与 PUK 预算。
- `HandleAPDU`：分发 `INS=0x20`（`Data` 为 PIN）与 `INS=0x2C`（`Data=PUK||新PIN`）。
- `RetryCounters`：读取持久化的剩余 PIN/PUK 次数。

## 断电模型

状态记录采用双槽 A/B 日志：每次提交只写当前未持有最新记录的槽，记录带 CRC 和递增代数。任何一次 `SlotNVM.WriteSlot` 都可被 `FaultNVM` 在写前、写后或撕裂写处打断；重启后取最高代数的合法记录。若两个槽都无效则 `ErrCorruptState` 失败关闭，不会把计数重置为上限。

计算顺序固定为：加载状态 → 扣减并提交 → 常数时间比较 → 成功后提交恢复。提交返回错误后立即停止，不进行任何比较或恢复。
