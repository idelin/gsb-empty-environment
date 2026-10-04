# ledger — 仅标准库的追加式账簿

审计友好的本地记账账簿：金额用整数分，记录定长、字段顺序固定，
哈希链保证历史交易位置不可变，冲正只追加不删除。

## 记录格式（145 字节，大端序，字段顺序固定）

| 字段        | 类型      | 字节 | 说明                       |
|-------------|-----------|-----:|----------------------------|
| seq         | uint64    |    8 | 序号，从 1 开始连续        |
| kind        | uint8     |    1 | 1=fund 2=transfer 3=reverse|
| reqID       | [16]byte  |   16 | 请求 ID（幂等键）          |
| refSeq      | uint64    |    8 | 冲正引用的原交易序号       |
| acctA       | [16]byte  |   16 | 账户 A                     |
| deltaA      | int64     |    8 | 账户 A 变化（分）          |
| acctB       | [16]byte  |   16 | 账户 B（可空）             |
| deltaB      | int64     |    8 | 账户 B 变化（分）          |
| prevDigest  | [32]byte  |   32 | 前一记录摘要               |
| digest      | [32]byte  |   32 | 以上全部字段的 SHA-256     |

## 语义

- **原子提交**：一次转账 = 余额校验 + 一条定长记录单次 `write` + `fsync`，
  随后才更新内存状态；余额不足整体拒绝，不会只写一半。
- **幂等**：请求 ID 是幂等键。同 ID 同内容重放返回原序号；
  同 ID 不同内容返回 `ErrConflict`。幂等表由记录本身重建，重开文件仍有效。
- **冲正**：`Reverse` 引用已提交的 transfer，追加一条反向新记录，
  原记录不动；每笔交易只能冲正一次；冲正会导致任一账户为负时整体拒绝。
- **崩溃恢复**：`Open` 只丢弃不完整的尾部记录；完整记录摘要损坏时
  以 `*CorruptError` 报告偏移和序号；`Recover` 截断到合法前缀并给出报告。
- **快照与导出**：`BalancesAt(seq)` 重放 1..seq 得到历史余额；
  `Export(w)` 输出 JSON Lines。
- **独立审计**：`Verify(path)`（reader.go）自行解析字节、重算哈希链和
  余额，不调用写入器的任何状态更新函数，并检查余额不为负。

## 命令行样例

```console
$ go build -o ledger-bin ./cmd/ledger
$ ./ledger-bin fund demo.ledger req-fund-1 alice 10000
committed seq=1
$ ./ledger-bin fund demo.ledger req-fund-2 bob 3000
committed seq=2
$ ./ledger-bin transfer demo.ledger req-tx-1 alice bob 2500
committed seq=3
$ ./ledger-bin transfer demo.ledger req-tx-1 alice bob 2500   # 幂等重放
committed seq=3
$ ./ledger-bin transfer demo.ledger req-tx-1 alice bob 9999   # 同 ID 不同内容
error: request ID already used with different content: request ID replayed with different content
$ ./ledger-bin reverse demo.ledger req-rev-1 3
committed seq=4
$ ./ledger-bin balance demo.ledger alice
alice 10000
$ ./ledger-bin snapshot demo.ledger 3
alice 7500
bob 5500
$ ./ledger-bin verify demo.ledger
chain OK
alice 10000
bob 3000
$ ./ledger-bin export demo.ledger
{"seq":1,"kind":"fund","req_id":"req-fund-1","acct_a":"alice","delta_a":10000,"prev_digest":"0000...0000","digest":"d6b6...7ee3"}
{"seq":2,"kind":"fund","req_id":"req-fund-2","acct_a":"bob","delta_a":3000,"prev_digest":"d6b6...7ee3","digest":"3103...5fb4"}
{"seq":3,"kind":"transfer","req_id":"req-tx-1","acct_a":"alice","delta_a":-2500,"acct_b":"bob","delta_b":2500,"prev_digest":"3103...5fb4","digest":"cc20...1551"}
{"seq":4,"kind":"reverse","req_id":"req-rev-1","ref_seq":3,"acct_a":"alice","delta_a":2500,"acct_b":"bob","delta_b":-2500,"prev_digest":"cc20...1551","digest":"3d62...5025"}
```

## 测试

```console
$ go test -v ./...
--- PASS: TestMidWriteFailure (0.02s)
--- PASS: TestCorruptCompleteRecord (0.02s)
--- PASS: TestDuplicateReversal (0.03s)
--- PASS: TestReverseTransferConservation (0.03s)
--- PASS: TestReopenIdempotency (0.01s)
--- PASS: TestInsufficientFunds (0.02s)
--- PASS: TestSnapshotAndExport (0.02s)
PASS
ok  	ledger	0.348s
```
