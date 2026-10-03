# ledger — 不可变只追加事务审计账簿

单机离散时间审计日志账簿，仅使用 Go 标准库。

## 设计

- **区块结构**（`block.go`）：每个区块包含前序区块哈希、业务有效载荷
  （转账/冲正事务）、HMAC-SHA256 签名回执与自身哈希
  `SHA256(高度 ‖ 前序哈希 ‖ 载荷 ‖ 签名)`。离散时间由区块高度（逻辑时钟）表示。
- **只追加**（`ledger.go`）：`append` 是唯一写入入口，历史区块物理上不可覆写。
  局部撤销通过 `Reverse` 追加逆向冲正条目（金额为原值取负、`Ref` 指向原区块），
  并同步更新总账；同一笔转账禁止重复冲正，冲正条目不可再被冲正。
- **默克尔证明**（`merkle.go`）：每次追加后保存该版本的默克尔根
  （`roots[i]` = 前 i+1 个区块的根），因此可为**任意历史版本**中的任意区块
  生成路径证明（`Prove(index, version)`），由 `VerifyProof` 独立校验。
- **完整性校验**：`VerifyBlock` 重算单区块哈希/前序链接/签名；
  `VerifyChain` 全量校验哈希链与所有历史版本的默克尔根。

## 运行

```sh
export GOCACHE=/private/tmp/gocache   # 本机沙箱需要，一般环境可省略
go test ./... -v                      # 测试
go run cmd/demo/main.go               # 验收场景演示
```

## 验收结果

测试（`ledger_test.go`，6 个用例全部通过）：

```
--- PASS: TestHashChain100                  100 笔事务哈希链完整
--- PASS: TestReversalAcceptance            冲正主场景（见下）
--- PASS: TestDoubleReverseRejected         禁止重复冲正/冲正冲正条目
--- PASS: TestTamperDetection               篡改历史区块即校验失败
--- PASS: TestProofEveryBlockEveryVersion   任意版本任意区块均可出证
--- PASS: TestProofRejectsWrongLeaf         证明对错误叶子不成立
```

`TestReversalAcceptance` 覆盖验收边界：写入 100 笔转账 → 对第 50 笔
（高度 49）冲正 → 账簿新增第 101 笔冲正记录并更新总账 → 原第 50 笔
内容未被覆写，其版本 100 的历史默克尔证明与版本 101 的新证明均有效，
冲正后全链校验通过。

演示输出（`go run cmd/demo/main.go`）：

```
[1] 已写入 100 笔转账，高度=100，哈希链校验=true
[2] 原第 50 笔: #49 TRANSFER acct-09 -> acct-00  amount=149 ...
[3] 新增第 101 笔冲正: #100 REVERSAL acct-00 -> acct-09  amount=-149 ref=49 ...
[4] 原区块未被覆写: true
    版本100 历史证明有效: true
    版本101 新版证明有效: true
    冲正后全链校验: true（高度=101）
```
