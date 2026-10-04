# ledger — 仅标准库的 Go 追加式账簿

金额为整数分。每条记录定长 120 字节、字段顺序固定（大端）：

| 字段 | 类型 | 字节 |
|---|---|---|
| Seq | uint64 | 8 |
| RequestID | [16]byte | 16 |
| From | uint64 | 8 |
| To | uint64 | 8 |
| Amount | int64（分，>0） | 8 |
| ReversalOf | uint64（0=普通转账） | 8 |
| PrevHash | [32]byte | 32 |
| Hash | [32]byte（前 88 字节的 SHA-256） | 32 |

## 语义

- 一次转账与其审计记录是同一笔落盘字节：整条记录一次 `Write` + `fsync`，
  余额在落盘成功后才变动；写失败回截文件，绝不写一半。
- 余额不足在写入前拒绝；同 RequestID 重放返回原记录，内容不同报 `ErrConflict`。
- 冲正引用已提交交易、只允许一次，作为新记录追加（不删原记录）；
  冲正资金不足则整体拒绝，不会只写一半。
- 账户 0 是保留的外部/金库账户：从它转出视为注资，豁免余额检查；
  包含 0 号账户在内的总账仍然守恒。
- 打开文件时重放校验哈希链：尾部不足一条记录的字节（崩溃残尾）直接截断；
  完整记录摘要损坏返回 `*CorruptError`，报告字节偏移与序号。
- `Verify`/`VerifyFile` 是独立读取器：只从原始字节重算余额与哈希链，
  不调用写入器的任何状态更新函数。

## CLI

```
ledger transfer <file> <req-hex32> <from> <to> <cents>
ledger reverse  <file> <req-hex32> <orig-seq>
ledger balance  <file> <acct>
ledger snapshot <file> <seq>
ledger export   <file> <out>
ledger verify   <file>
```

## 测试

```
go test ./... -v
```
