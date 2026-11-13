# 两服务器 XOR-PIR 与响应完整性

本仓库实现了题目要求的 Go PIR 包。数据库包含 `1 <= N <= 4096` 条 32 字节记录；可信发布方生成 `tag_i = HMAC-SHA256(key, be32(i) || record_i)`，服务器保存 `record_i || tag_i` 的 64 字节行。客户端使用两台不串通服务器的 XOR PIR，并在最终恢复目标行后验证行 tag。

## 快速开始

```go
db, err := netsec.Publish(records, hmacKey, "snapshot-2026-11-14")
if err != nil {
    // ...
}

pub1, priv1, _ := netsec.GenerateServerIdentity()
pub2, priv2, _ := netsec.GenerateServerIdentity()
server1, _ := netsec.NewServer(db, priv1)
server2, _ := netsec.NewServer(db, priv2)

client, err := netsec.NewClient(
    db.Registry(),
    hmacKey,
    [2]ed25519.PublicKey{pub1, pub2},
    nil, // nil 使用 crypto/rand.Reader
)
if err != nil {
    // ...
}

record, err := client.Query(7, server1, server2)
if err != nil {
    // Query 的所有失败都恰为 netsec.ErrVerification。
}
```

`records` 的类型为 `[][]byte`，每条必须正好 32 字节。HMAC 密钥只交给可信发布方和客户端，服务器不需要也不应持有它。

## 协议

### 发布与快照

- 行布局固定为 `record[32] || tag[32]`。
- HMAC 输入固定为 `big-endian uint32(index) || record`，没有额外分隔或字符串编码。
- Merkle 树的叶子为 `SHA-256(0x00 || row)`，内部节点为 `SHA-256(0x01 || left || right)`；奇数节点在下一层复制最后一个节点。
- 两台服务器必须从同一个 `Database` 快照构造，因而持有相同的 `SnapshotID`、`N` 和 `MerkleRoot`。
- Merkle 根只用于把服务器绑定到已登记数据库快照。目标行的完整性由最终行的 HMAC tag 保证，根不能替代逐行 tag。

### 查询向量

客户端每次查询索引 `j`：

1. 从宿主随机源读取 `ceil(N/8)` 字节得到 `q0`。
2. 把最后一个字节中索引 `>= N` 的尾部位清零。因此 `q0` 在全部 `2^N` 个 N 位向量上均匀分布。
3. 计算 `q1 = q0 XOR e_j`。
4. 位编号固定为：索引 `i` 位于字节 `i/8` 的第 `i%8` 位（最低位优先）。

服务器只接受长度恰为 `ceil(N/8)` 且尾部多余位全为零的向量，否则返回 `ErrMalformedQuery`。

### 响应与签名

每台服务器计算所有置位行的 64 字节逐字节 XOR，并返回：

- XOR 响应 `payload[64]`
- `SnapshotID`
- 数据库 `MerkleRoot`
- Ed25519 公钥身份 `ServerID`
- Ed25519 签名

签名消息使用固定域分离和长度框架：

```text
query_digest = SHA-256("example.org/netsec/q0105/query/v1\0"
                       || be32(N) || be32(len(query)) || query)
signature_msg = "example.org/netsec/q0105/response/v1\0"
                || query_digest
                || be32(64) || payload
                || be32(len(SnapshotID)) || SnapshotID
```

响应签名绑定查询向量摘要、响应内容和快照 ID。客户端另外固定信任两台服务器的公钥；`MerkleRoot` 通过与本地登记值比较来认证，而不是放进响应签名。

### 客户端校验

`Client.Query` 按以下条件校验，任意一步失败都只返回同一个 `ErrVerification`，不泄露失败原因：

- 索引在 `[0, N)` 内，随机源读取成功。
- 两个响应的公钥分别等于客户端固定的两个服务器身份。
- 两个服务器身份互不相同。
- 两个 Ed25519 签名在各自的查询向量、响应和快照 ID 上验证通过。
- 两个响应的 `SnapshotID` 与登记快照一致。
- 两个响应的 `MerkleRoot` 与登记根一致。
- `payload0 XOR payload1` 的 64 字节行通过 `tag_j` 的 HMAC 校验。

客户端从不区分“签名错误、快照错误、服务器身份相同、tag 错误”等失败路径。

## 恶意服务端夹具

`MaliciousServer` 使用真实服务器私钥签名，覆盖以下攻击：

- `AttackFlipPayloadSigned`：翻转响应一位后重新签出有效 Ed25519 签名，由最终 HMAC tag 拒绝。
- `AttackFlipPayloadUnsigned`：签名后翻转一位，由签名校验拒绝。
- `AttackChangeSnapshot`：修改快照 ID 并签出有效签名，由固定快照检查拒绝。
- `AttackChangeMerkleRoot`：修改声明的 Merkle 根，由登记根检查拒绝。
- `AttackCorruptSignature`：破坏真实签名，由签名校验拒绝。
- `AttackReplay` / `NewReplayServer`：返回另一查询的真实签名响应，由查询摘要绑定拒绝。
- `NewSameIdentityServers`：两端使用同一身份，两个签名都有效，由身份不同检查拒绝。

测试还独立重算 HMAC，检查大端索引编码、行长和 tag 位置；所有篡改场景都断言返回 `ErrVerification`，合法查询则返回原始 32 字节记录。

## 向量分布与隐私边界

对固定 `N` 和 `j`，枚举全部 `2^N` 个 `q0` 可得到：

- 服务器 0 看到的 `q0`：每个 N 位向量恰好出现一次。
- 服务器 1 看到的 `q1 = q0 XOR e_j`：`x -> x XOR e_j` 是向量空间上的双射，因此每个 N 位向量也恰好出现一次。
- 每个向量的概率都是 `1 / 2^N`，对任意 `j` 都相同。

`DISTRIBUTION.md` 记录了 `N = 1..8` 的完整枚举结果；`distribution_test.go` 对每个 `N <= 8`、每个 `j` 实际枚举并检查上述计数一致。该证明假设宿主随机源在 N 位上均匀。

本题只提供两服务器不串通模型下的查询隐私。若两台服务器交换 `q0` 与 `q1`，立即得到 `q0 XOR q1 = e_j` 并恢复 `j`。本实现**不声称**防两服务器串通，也不对请求时序、响应大小或服务器执行时间作隐藏承诺。

## 运行测试

```sh
go test ./...
go test -cover ./...
go vet ./...
```
