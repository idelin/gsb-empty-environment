# stepup

身份服务允许用户更换二次验证设备，现有登录会话可能在换绑期间越过升级认证。给Go服务实现会话升级组件，挑战绑定会话、设备代次与目标操作；换绑应使未完成挑战失效，成功回执只能兑换一次且不能用于另一操作。用可控时钟和并发屏障覆盖先验证后换绑、先换绑后兑换及过期与兑换同刻。

## 设计

- `Service` 是内存态会话升级组件，全方法由单一互斥锁串行化，状态转换均在锁内完成。
- 每个挑战绑定四个要素：登录会话 `sessionID`、创建时的设备代次 `generation`、目标操作 `operation`、过期时刻 `expiresAt`。
- `RebindDevice` 递增设备代次，并把该用户所有未兑换的挑战（含已验证待兑换的回执）置为 `invalidated`；已兑换记录保留，不影响已完成操作。
- 挑战与回执共用同一条状态机记录（`pending → verified → redeemed`，换绑时进入 `invalidated`），回执 id 即挑战 id，天然单次使用。
- 过期判定为左闭右开：`now >= expiresAt` 即过期，到期时刻的验证与兑换一律判负。
- 时间源为 `Clock` 接口，生产用 `SystemClock`，测试用可手动推进的 `FakeClock`。

## 流程

1. `BeginChallenge(userID, sessionID, operation)` 创建待验证挑战，记录当前代次与 TTL。
2. `SubmitFactor(challengeID, sessionID, code)` 校验二次因子（因子校验在锁外执行，返回后锁内二次复核，避免校验期间被换绑/过期）。
3. `Redeem(receiptID, sessionID, operation)` 在锁内逐条复核代次、会话、操作、过期与状态，通过后原子置为 `redeemed`；重复兑换返回 `ErrAlreadyRedeemed`，跨操作返回 `ErrOperationMismatch`。
4. `RebindDevice(userID)` 换绑设备，未完成挑战立即失效，之后兑换返回 `ErrGenerationMismatch`。

## 测试

`go test -race ./...` 覆盖：

- 先验证后换绑、先换绑后兑换（含等待窗口内换绑与多次换绑）。
- 过期边界：到期前一纳秒成功、到期同刻失败、到期同刻提交因子失败。
- 并发屏障：兑换临界区内时钟推到过期时刻、16 个兑换在锁外屏障汇合后同时过期、32 路并发兑换同一回执仅一次成功、换绑与 8 路兑换并发 25 轮不出现跨代次成功。
