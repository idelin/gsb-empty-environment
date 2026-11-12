# 策略文件BOM双解析

策略文件会先被签名校验端按行读取，再交给运行端解析，攻击者在第二行行首插入 U+FEFF，可让一端把 allow 当作未知键、另一端剥离后当作有效规则。写两端共用的 Go 策略词法包，实现 BOM 规则：只允许文件首部带一个 UTF-8 BOM 并只剥离一次；其他位置的 U+FEFF、UTF-16 或 UTF-32 BOM 都拒绝，规则哈希按剥离后的字节计算。用首部 BOM、行首 BOM、键内 BOM 和无 BOM 文件验证两端结论一致。

## 共享词法包

`example.org/netsec/q0271/policy` 是两端唯一的策略字节入口：

- 签名校验端调用 `policy.LexFile`，只使用规范化后的 `Document.Lines` 与 `Document.Hash`。
- 运行端调用 `policy.ParseFile`；它内部先调用 `Lex`，再解析规则。
- 两端都不会自行剥离 BOM，因此不存在“一端剥离、另一端不剥离”的分歧。

```go
doc, err := policy.LexFile("policy.conf")   // 校验端：按行读取 + 哈希
parsed, err := policy.ParseFile("policy.conf") // 运行端：解析 allow/deny
```

## BOM 规则

- 文件偏移 0 处允许恰好一个 UTF-8 BOM（`EF BB BF`），并且只剥离一次。
- 剥离后对整个剩余字节序列扫描，任何位置的 U+FEFF（同样是 `EF BB BF`）都拒绝，包括行首、键内和值内。
- UTF-16 BOM（`FE FF`、`FF FE`）和 UTF-32 BOM（`00 00 FE FF`、`FF FE 00 00` 及 2143/3412 变体）在任何位置都拒绝。
- 仅允许合法 UTF-8；非法编码拒绝。
- 摘要为剥离后字节的 SHA-256，不包含被剥离的首部 BOM，也不包含原始校验和。

支持的规则行格式为 `allow <value>` 或 `deny <value>`，也接受 `allow=<value>` / `allow:<value>`；空行和 `#` 开头的行忽略，未知键报错并带上行号。

## 验证

```sh
go test ./...
```

`policy/policy_test.go` 会用真实文件分别走校验端和运行端入口，覆盖无 BOM、首部 BOM、行首 BOM、键内 BOM、重复 BOM、UTF-16/32 BOM 与非法 UTF-8，并断言两端接受/拒绝结论一致、哈希一致。
