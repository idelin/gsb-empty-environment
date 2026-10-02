# Merkle Patricia Trie (MPT)

可验证前缀树：字典树（前缀压缩）与默克尔树（哈希承诺）的结合。
仅使用 Go 标准库（`crypto/sha256`）。

## 结构

- `LeafNode` — 保存剩余键后缀与值（路径压缩的终点）
- `ExtensionNode` — 压缩的共享路径（单一路径段，无分支）
- `BranchNode` — 16 进制 nibble 分支，可携带终止值

每个节点的哈希由其规范编码（含子节点哈希）经 SHA-256 得出，
因此任何节点的变更都会级联改变根哈希，伪造字典会被立即识破。

## API

- `Put(key, value)` — 插入/更新，自动收缩共享路径
- `Get(key)` — 精确查找
- `GetByPrefix(prefix)` — 按键前缀检索全部键值对
- `RootHash()` — 默克尔根
- `Prove(key)` — 输出从根到叶的完整节点序列证明
- `VerifyProof(rootHash, key, proof)` — 校验证明并返回对应值

## 运行

```sh
go test ./... -v   # 测试
go run ./cmd/demo  # 演示
```

## 运行样例（`go run ./cmd/demo`）

```
=== Trie structure (paths shown as hex nibbles) ===
Ext      path=06 hash=cc31a4a24f789ef5
  Branch   hash=9943fecb24eac881
    [1]
      Ext      path=06030603060f0705060e0704020e hash=259ee970c08c1ad2
        Branch   hash=4eb0e405917de7e2
          [6]
            Branch   hash=36622a0929451582
              [2]
                Leaf     path=0601060c0601060e06030605 value="1000" hash=1f3a2146795a236d
              [e]
                Leaf     path=060f060e06030605 value="7" hash=fc204d0496e3a6ad
          [7]
            Ext      path=030704060f0702060106070605020e hash=7ca110c1490b63a4
              Branch   hash=80798e768724851f
                [6]
                  Leaf     path=03060f06040605 value="0x60806040" hash=1d25a9799f0600fa
                [7]
                  Leaf     path=02060f060f0704 value="0x56e81f17" hash=7df78c5288f978bb
    [2]
      Ext      path=060c060f0603060b020e hash=3712a75d0f7bea75
        Branch   hash=489ef512d74ee304
          [6]
            Leaf     path=0e0705060d060206050702 value="19283746" hash=f122206153382efe
          [7]
            Leaf     path=040609060d0605070307040601060d0700 value="1759420800" hash=b8d2ca04a4983ae5

Root hash: cc31a4a24f789ef5705b49cf399da1f4d35261d7ccee4a18bfecd23a0d608bd1

=== Prefix search: "account.storage." ===
  account.storage.code = 0x60806040
  account.storage.root = 0x56e81f17

=== Merkle proof for "account.balance" (exists=true, 6 nodes root->leaf) ===
  [0] ExtensionNode hash=cc31a4a24f789ef5705b49cf399da1f4
  [1] BranchNode    hash=9943fecb24eac881fe5eb0e909c666fb
  [2] ExtensionNode hash=259ee970c08c1ad2c8ef4fc6d1a8f9fd
  [3] BranchNode    hash=4eb0e405917de7e2580142d18b781757
  [4] BranchNode    hash=36622a0929451582953ade9ce258de59
  [5] LeafNode      hash=1f3a2146795a236d3020b3ccec25dc09
  verify OK -> "account.balance" = 1000

=== Tamper test: corrupt a parent node's hash in the proof ===
  detected: mpt: step 1: node content does not match its hash (tampered node)

=== Root hash cascade: update one value ===
  before: cc31a4a24f789ef5705b49cf399da1f4
  after : 0b8382a3f11f344b1ad4baad8cdafcee
```

## 测试结果（`go test ./... -v`）

```
=== RUN   TestInsertAndGet
--- PASS: TestInsertAndGet (0.00s)
=== RUN   TestPathCompression
--- PASS: TestPathCompression (0.00s)
=== RUN   TestExtensionSplit
--- PASS: TestExtensionSplit (0.00s)
=== RUN   TestPrefixSearch
--- PASS: TestPrefixSearch (0.00s)
=== RUN   TestRootHashCascadeAndDeterminism
--- PASS: TestRootHashCascadeAndDeterminism (0.00s)
=== RUN   TestProofForAccountBalance
--- PASS: TestProofForAccountBalance (0.00s)
=== RUN   TestProofTamperedParentHashDetected
--- PASS: TestProofTamperedParentHashDetected (0.00s)
=== RUN   TestProofTamperedValueDetected
--- PASS: TestProofTamperedValueDetected (0.00s)
=== RUN   TestProofRejectsForeignRoot
--- PASS: TestProofRejectsForeignRoot (0.00s)
=== RUN   TestNonMembershipProofFails
--- PASS: TestNonMembershipProofFails (0.00s)
PASS
ok  	mpt
```
