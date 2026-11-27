# TreeKEM / RFC 9420 状态机包

该目录实现了一个仅依赖 Go 标准库的 RFC 9420 TreeKEM 数学与提交状态机模型。
密码原语包括 X25519 ECDH、Ed25519、HKDF-SHA256、HMAC 和 AES-256-GCM；没有第三方依赖，也不发起任何网络访问。

## LBBT 算术

`Index` 是紧凑左平衡二叉树节点下标：

- `Leaf(position)` / `leaf(position)`：叶节点 `2*position`
- `NodeIndexAt(level, position)` / `node(level, position)`：RFC `node(level,index)`
- `Level(index)` / `level(index)`：尾随 1 比特数
- `Left(index)`、`Right(index)`：理想树子节点
- `Parent(index)`：理想树父节点
- `RootIndex(leafCount)`、`ParentBounded`、`LeftBounded`、`RightBounded`
- `DirectPath(leaf, leafCount)`、`Copath(leaf, leafCount)`

非完整 LBBT（例如 5、6、7 叶）的越界理想右孩子会通过 `RightBounded` 精确回绕到紧凑数组中的实际孩子。

## Resolution 与父哈希

- `RatchetTree.Resolution(index)` / `Resolution(tree,index)` 实现空白节点递归和 `unmerged_leaves` 直达路径解析。
- `ComputeParentHash` 使用 RFC 风格 `MLS 1.0 Parent Hash` 域分离和 `ParentHashInput` 字段。
- `VerifyParentHashChain` 校验单个后代叶到根的递归链。
- `VerifyParentHashes` 要求每个非空父节点至少有一个经签名叶节点支持的无篡改见证链。
- `VerifyCommittedTree` 额外要求当前 Commit 提交者的链完整到根，可拦截当前路径上的空白化。
- `VerifyTree(tree, trustedTreeHash)` 用签名 GroupContext 中的树哈希锚定任意历史节点空白化。

## Commit 状态机

`Host`（别名 `Group`）维护一个成员的树、纪元、转录哈希、`init_secret` 和父节点私钥环：

1. `NewGroup`
2. `NewIdentity` + `Identity.KeyPackage`
3. `QueueAdd` / `QueueRemove` / `RotateIdentityAndQueueUpdate`
4. `Commit` 生成签名 Commit、TreeKEM `UpdatePath` 和新成员 `Welcome`
5. 现有成员调用 `AcceptCommit`
6. 新成员调用 `JoinFromWelcome`

`UpdatePath` 从首个父步骤生成随机秘密，随后用 RFC 标签链式派生；每个父节点私钥由路径秘密确定性派生。每个路径步骤向精确 copath resolution 的每个节点封装一份路径秘密，接收者集合经过索引、公钥和活动叶集合三重校验。

Remove 会空白化被移除叶及其整条直达路径，使被移除成员曾持有的父私钥不会再作为 resolution 接收节点；新路径的接收叶集合由提交后树重新计算，额外信封会被 `ValidatePathRecipients` 拒绝。

## 纪元调度

`JoinerSecret(initSecret, commitSecret)` 计算 `HKDF-Extract`，`DeriveEpochSecrets` 执行：

```text
epoch_secret = ExpandWithLabel(joiner_secret, "epoch", GroupContext, 32)
sender data / encryption / exporter / authentication /
external / confirm / membership / resumption / init
```

`Transcript` 维护 confirmed/interim 转录哈希，Confirmation HMAC 绑定新的 confirmed transcript hash。

## 最小示例

```go
alice, _ := treekem.NewGroup([]byte("group-id"), "alice")
bobID, _ := treekem.NewIdentity("bob")
kp, _ := bobID.KeyPackage()
_ = alice.QueueAdd(kp)

commit, welcomes, _ := alice.Commit()
bob, err := treekem.JoinFromWelcome(bobID, welcomes[0], commit)
```

导入模块根路径后可将包名别名为 `mls` 或 `treekem`。

## 测试

```sh
go test -count=1 ./...
```

覆盖索引向量、任意 1..32 叶的 direct/copath 性质、unmerged resolution、幽灵节点、父哈希篡改、恶意空白化、被移除成员离线解密、批量 Add 和多纪元 Add/Update/Remove 演进。
