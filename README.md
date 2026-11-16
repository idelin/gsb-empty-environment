# 安全聚合份额披露互斥

安全聚合协议里，恶意服务器可以对同一个用户先说它掉线了，让客户端交出它的成对种子份额，再说它还在线，让客户端交出它的自掩码份额，两份凑齐后个人输入就暴露了。为Go联邦学习客户端写可导入的份额披露守卫，阶段为广播密钥、分享、掩码输入、一致性签名、解密：客户端在掩码输入阶段记录见到的存活集合 U2；一致性阶段收到服务器给出的存活集合 U3 与其他客户端的签名，须满足 U3 是 U2 的子集、U3 的大小不小于阈值 t，并校验至少 t 个对 U3 的签名，签名者均在 U3 内；解密阶段服务器请求披露：对掉线集合 D 的用户披露其成对种子份额，对存活集合 S 的用户披露其自掩码份额，S 必须等于已签名的 U3，D 必须等于 U2 去掉 U3，二者不得相交；对同一用户只能披露一种份额，已披露一种后再请求另一种必须拒绝并终止会话；会话终止后不得再披露任何份额；一次会话只回应一次解密请求。用同一用户先 D 后 S、S 与 U3 不等、签名不足、U3 不是 U2 子集和重复请求验证。

## shareguard 包

可导入的守卫位于 `shareguard`，导入路径 `example.org/netsec/q0835/shareguard`，
只依赖标准库。核心类型 `shareguard.Guard` 是单个安全聚合会话的策略引擎，
本身不保存任何秘密材料，仅决定某次披露是否放行。

阶段按 `PhaseBroadcastKey -> PhaseShare -> PhaseMaskedInput ->
PhaseConsistency -> PhaseDecryption` 单调推进，通过 `AdvanceTo` 切换。

### 关键 API

- `NewGuard(t, verifier)`：以阈值 t 和 `SignatureVerifier` 创建守卫。
- `ObserveParticipant(id)`：掩码输入阶段记录存活集合 U2。
- `AcceptConsistencyStatement(u3, sigs)`：一致性阶段校验 U3⊆U2、|U3|≥t、
  签名者均在 U3 内且对 U3 的有效签名至少 t 个，通过后锁定 U3。
- `AcceptDecryptionRequest(dropped, surviving)`：解密阶段唯一一次请求，
  要求 `S == U3`、`D == U2\U3` 且 D∩S=∅；任何不符都终止会话。
- `DisclosePairwise(user)` / `DiscloseSelfMask(user)`：分别授权披露成对
  种子份额（D 内用户）与自掩码份额（S 内用户）。同一用户跨类型请求返回
  `ErrDoubleDisclosure` 并终止会话；同类型重复请求幂等成功。
- `Terminate(reason)` / `Terminated()` / `TerminationReason()`：手动终止
  与状态查询；终止后一切披露都返回 `ErrSessionTerminated`。

签名方案通过 `SignatureVerifier` 接口注入，包内提供 HMAC-SHA256 参考实现
`HMACVerifier` 与规范编码函数 `CanonicalAliveSet`，可替换为 Ed25519 等方案。

### 测试

`go test ./... -race -cover` 覆盖题目要求的全部攻击场景：

- 同一用户先 D 后 S 的跨类型双披露（`TestDoubleDisclosure_DroppedThenSurviving`）
- S 与 U3 不等（`TestSurvivingSetNotEqualToU3`）
- 签名不足、重复签名凑数、伪造签名（`TestConsistency_NotEnoughSignatures`）
- U3 不是 U2 子集、签名者不在 U3 内（`TestConsistency_U3NotSubsetOfU2`）
- 重复解密请求（`TestDuplicateDecryptionRequest`）

可运行的端到端集成示例见 `shareguard.ExampleGuard`。
