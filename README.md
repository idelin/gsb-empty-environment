# SCSI预留端口绑定

集群靠 SCSI-3 持久预留做存储隔离，被攻陷的节点可抢占或清除他人的预留，用偷来的密钥从另一个端口登记。为Go存储目标写供持久预留请求分发调用的预留状态机：采用 Write Exclusive–Registrants Only，登记的密钥绑定发起端的端口标识，PREEMPT 只允许已登记且密钥匹配的发起端，CLEAR 仅限管理策略指定的端口，被抢占者的登记随之移除，预留代数每次变化加一，APTPL 位决定掉电后是否保留。用换端口复用密钥、未登记抢占、CLEAR 越权、掉电与代数验证。

## 实现

- `reservation.go`：WERO 持久预留状态机，统一入口为 `Machine.Dispatch(Request)`，
  另有 `Register`/`Reserve`/`Release`/`Preempt`/`PreemptAndAbort`/`Clear`/
  `ReadKeys`/`ReadReservation`/`PowerLoss`/`WriteAllowed` 等方法。
- 密钥与发起端端口标识（`PortID`，由传输层认证填入，请求方无法伪造）绑定，
  反向索引 `keyOwners` 保证同一密钥不能被第二个端口登记。
- PREEMPT 先经 `authenticate` 校验“已登记 + 端口绑定密钥匹配”，被抢占者登记
  立即移除；若其持有预留则转给抢占者；PREEMPT AND ABORT 额外返回 `Aborted`。
- CLEAR 只接受 `NewMachine(clearPorts...)` 管理策略白名单中的端口，越权请求
  不改变任何状态。
- 预留代数 `Generation` 每次成功的状态变更加一，失败请求与只读请求不加。
- APTPL 按登记保存，`PowerLoss` 仅保留 APTPL 登记；预留仅在持有者仍有 APTPL
  登记时保留，全部状态丢失时代数归零。
- `reservation_test.go` 覆盖换端口复用窃取密钥、未登记/错密钥抢占、CLEAR
  越权、掉电 APTPL 保留与代数递增等场景。
