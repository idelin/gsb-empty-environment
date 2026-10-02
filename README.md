# erasureproof — 可擦除合规数据脱敏证明器

纯 Go 标准库实现的**可验证擦除** Merkle 存证树，满足"数据被遗忘权"类隐私法规：
客户注销时，其叶子被重写为携带**删除签名**的 `Erase_Marker`，树根平滑演进，
其余客户的存证路径依然完全有效可验，且原个人数据被物理清零。

## 设计

- **叶子承诺**：`leaf = H(0x00 || "RECORD" || len(id) || id || data)`，绑定客户身份与数据。
- **Erase_Marker**：`marker = H(0x00 || "ERASED" || len(id) || id || sig)`，
  任何审计方仅凭 `(id, sig)` 即可重算，无需接触已删除的个人数据。
- **删除签名**：合规机构用 P-256 ECDSA 私钥对 `context || id || oldLeafHash` 签名，
  证明擦除经过授权；签名本身成为 Marker 的一部分。
- **物理擦除**：`Erase` 将个人数据的底层字节数组**原地清零**后再解除引用。
- **平滑演进**：每次擦除产生一条 `RootTransition` 审计记录（旧根 → 新根、
  旧叶子路径、Marker 路径、签名），`VerifyTransition` 可证明"恰好替换了那一片叶子"。
- **域分离**：叶子 `0x00` / 内部节点 `0x01` 前缀，防止证明混淆攻击。

## 核心 API

| 函数 | 作用 |
| --- | --- |
| `Prover.Add(id, data)` | 提交客户记录为叶子 |
| `Prover.Prove(id)` / `VerifyInclusion(root, proof)` | 出具并验证存证路径 |
| `Prover.Erase(id)` | 授权擦除：清零数据、重写 Marker、演进根 |
| `Prover.ErasureProof(id)` / `VerifyErasure(pub, root, ep)` | 验证某叶子已是合法 Marker |
| `VerifyTransition(pub, tr)` | 审计单步根演进 |

## 运行样例

```console
$ go run ./cmd/demo
[1] 4 customers committed, root = fbf9078315263af4…
[2] inclusion proof cust-dave   valid=true
[2] inclusion proof cust-alice  valid=true
[2] inclusion proof cust-bob    valid=true
[2] inclusion proof cust-carol  valid=true
[3] cust-alice erased: root fbf9078315263af4… -> f6ab866a51dd01bf… (epoch 0)
[4] alice's data after erasure: erasureproof: record already erased; original buffer wiped=true
[5] erasure proof for cust-alice valid=true
[6] root transition (old root -> new root) valid=true
[7] post-erasure proof cust-bob    valid=true
[7] post-erasure proof cust-carol  valid=true
[7] post-erasure proof cust-dave   valid=true
```

## 测试结果

```console
$ go test ./... -v
=== RUN   TestInclusionProofsVerify
--- PASS: TestInclusionProofsVerify (0.01s)
=== RUN   TestEraseKeepsOtherProofsValid
--- PASS: TestEraseKeepsOtherProofsValid (0.00s)
=== RUN   TestErasureProofVerifies
--- PASS: TestErasureProofVerifies (0.00s)
=== RUN   TestPhysicalErasure
--- PASS: TestPhysicalErasure (0.00s)
=== RUN   TestRootTransitionChain
--- PASS: TestRootTransitionChain (0.00s)
=== RUN   TestTamperedInclusionProofFails
--- PASS: TestTamperedInclusionProofFails (0.00s)
=== RUN   TestErrorPaths
--- PASS: TestErrorPaths (0.00s)
=== RUN   TestMerkleTreePaddingAndProofBoundaries
--- PASS: TestMerkleTreePaddingAndProofBoundaries (0.00s)
PASS
ok  	erasureproof	0.645s
```

覆盖：存证证明往返、擦除后其余证明全部有效（验收边界）、擦除证明验签
（错误 ID / 错误机构密钥 / 错误根均被拒绝）、物理清零、根演进链审计、
篡改证明拒绝、错误路径、非 2 幂叶子填充。

## 文件

- `merkle.go` — Merkle 树、包含证明生成与验证
- `prover.go` — Prover、擦除、签名、验证器、根演进审计
- `prover_test.go` — `go test` 测试
- `cmd/demo/main.go` — 端到端运行样例
