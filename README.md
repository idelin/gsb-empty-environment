# auditledger — 不可变只追加事务审计账簿

单机离散时间审计日志账簿，纯 Go 标准库实现（`crypto/ed25519`、`crypto/sha256`、`encoding/json` 等）。

## 设计

- **区块结构**（`ledger.go` 中 `Block`）：每条事务 = 前序区块哈希（`PrevHash`，构成哈希链）+ 业务有效载荷（`Payload`）+ 签名回执（ed25519 签名）+ 本区块哈希。离散逻辑时间 `Time` 等于区块序号。
- **只追加**：`Ledger` 仅暴露 `AppendTransfer` / `AppendReversal`，历史区块不可修改；`GetBlock` 返回副本。
- **局部撤销 = 逆向冲正**：`AppendReversal(i)` 校验目标（须为未冲正过的转账）后追加一条 `reversal` 条目，把原转账金额逆向加回总账；原区块保持原样，绝不物理覆写。重复冲正、冲正冲正条目均被拒绝。
- **默克尔路径证明**：每次追加记录该版本（前 n 个区块）的默克尔根；`ProveAt(i, v)` 可为任意历史版本 v 下的区块 i 生成路径证明，`VerifyProof` 离线验证。叶子/内部节点使用 0x00/0x01 域分离前缀，奇数节点向上晋升。

## 运行

```sh
go test ./... -v     # 测试
go run ./cmd/demo    # 运行样例
```

## 运行样例输出

```
[1] 已写入 100 笔转账事务
    区块#0   hash=998da13d…d629d993 prev=00000000…00000000
    区块#99  hash=1c4dd69b…11387824 prev=9b933566…42caee12
    余额: alice=990000 bob=10000

[2] 第 50 笔证据: hash=509b300e…8974998a sig=6c6c881697694e6b…
    版本 100 默克尔根: ea85014d…4270595c (路径长度 7, 验证=true)

[3] 冲正完成: 新增区块#100 (type=reversal, reverses=49)
    账簿长度=101, 余额: alice=990100 bob=9900

[4] 冲正后审计:
    第 50 笔未被覆写: true (hash 仍为 509b300e…8974998a)
    历史版本 100 证明仍有效: true
    最新版本 101 证明有效:   true (根 6ce4901f…b1d51568)
    全链哈希与签名校验: 通过
```

## 测试结果

```
=== RUN   TestAcceptance            # 验收场景：100 笔转账 + 第 50 笔冲正 + 历史证明仍有效
--- PASS: TestAcceptance (0.04s)
=== RUN   TestReversalRules         # 禁止重复冲正/冲正冲正条目/冲正不存在区块
--- PASS: TestReversalRules (0.00s)
=== RUN   TestTransferRules         # 金额为正、余额充足、禁止自转
--- PASS: TestTransferRules (0.00s)
=== RUN   TestTamperDetection       # 篡改历史字段/证明均被检出
--- PASS: TestTamperDetection (0.00s)
=== RUN   TestMerkleProofAllSizes   # 1..64 规模下全部区块证明可验证
--- PASS: TestMerkleProofAllSizes (0.11s)
PASS
ok  	auditledger	0.533s
```
