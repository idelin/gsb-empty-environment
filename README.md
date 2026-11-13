# 路径签名链漏签了下一跳AS

路径验证只核对每个AS签了前缀和历史路径，却没绑定下一接收AS，攻击者可把合法更新转发给未授权邻居。实现供Go路由宿主调用的题内路径签名链：起源记录含prefix、originAS、pathID和目标peerAS；每跳记录signerAS、previousHash、nextPeerAS、pCount及Ed25519签名，签名输入按固定域标签绑定prefix、pathID、previousHash、signerAS、nextPeerAS和pCount。起源签名previousHash为零并要求signerAS=originAS；后续每记录previousHash等于前记录完整摘要，且前记录nextPeerAS等于本记录signerAS。接收时最后记录nextPeerAS必须等于本地AS，会话对端AS必须等于最后signerAS；pCount为1至宿主上限并展开成逻辑AS路径，禁止AS环和路径长度超限。每跳签名转录还绑定keyGeneration；每跳签名转录还绑定keyGeneration；公钥从按AS和keyGeneration登记的受信表取，签名记录绑定keyGeneration，撤销或过期键不接受。输出规范逻辑路径与每跳证据，任一失败零安装。用真实签名测试删跳、换nextPeer、重排记录、跨前缀搬运、pCount膨胀和密钥轮换，不能把签名链通过等同于商业关系有效。

## 实现

纯标准库（`crypto/ed25519`、`crypto/sha256`、`net/netip`）实现，无外部依赖。

- `pathsig.go`：记录类型 `HopRecord`、链 `SignedPath`、固定域标签 + 长度前缀的
  规范签名转录、`SignOrigin` / `SignHop`、记录完整摘要 `RecordDigest`。
- `registry.go`：按 `(AS, keyGeneration)` 登记的受信公钥表 `KeyRegistry`，
  支持登记、撤销，并按宿主时钟执行 `NotBefore` / `NotAfter` 有效期。
- `host.go`：路由宿主 `Host`。`VerifyCryptographic` 只做密码学校验并产出
  `VerifiedPath`（规范逻辑路径 `LogicalPath` + 每跳 `HopEvidence`）；`Install`
  先完整校验、再过商业关系策略，全部通过才写 RIB，任一失败即零安装。

## 关键绑定与校验

- 每跳签名转录在域标签 `PathsSig/v1/transcript` 下绑定
  `prefix、pathID、previousHash、signerAS、nextPeerAS、pCount、keyGeneration`，
  字段自带标签和长度前缀，杜绝字段拼接与跨前缀/跨 pathID 搬运。
- 起源记录 `previousHash = ZeroHash` 且 `signerAS = originAS`；后续记录
  `previousHash` 必须等于前记录的完整摘要（`PathsSig/v1/record-digest`，含签名），
  且前记录 `nextPeerAS` 必须等于本记录 `signerAS`。`SignHop` 在签名侧即强制
  `signer == 上一跳 nextPeer`，未授权 AS 无法接棒重签。
- 接收侧强制：末记录 `nextPeerAS == 本地AS`，BGP 会话对端 `== 末记录 signerAS`。
- `pCount ∈ [1, MaxPCountPerHop]`，按 AS prepend 展开为逻辑 AS 路径；展开长度受
  `MaxPathLength` 约束；跨跳重复 AS 判为环（同跳内连续重复是合法 prepend）。
- 公钥仅从受信表按 `(signerAS, keyGeneration)` 取；撤销、过期、尚未生效或未登记
  的密钥在校验签名之前即被拒绝，实现密钥轮换而旧代仍可独立受控。

## 安全边界

签名链通过 **只证明密码学来源与下一跳授权**，不代表商业关系有效。
`RelationshipPolicy` 与密码学校验完全分离：缺策略或策略拒绝时，即使签名全部合法
也不安装（`ErrPolicyNotConfigured` / `ErrBusinessPolicy`）。

## 测试

`go test ./...` 使用真实 Ed25519 密钥（非桩件），覆盖：删跳、篡改/合法重定向
nextPeer、重排记录、整条与单记录跨前缀搬运、pathID 替换、pCount 篡改/超上限/
长度膨胀、密钥轮换与撤销/过期/未生效/未知代、AS 环、起源规则、会话对端与最终
接收者绑定、非规范前缀，以及失败更新零安装和密码学通过但商业策略拒绝。
