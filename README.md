# 通配符展开与RRSIG标签数

攻击者让解析器把通配符展开的应答当成具体名字存在。为Go验证包写可导入的 RRSIG 通配符判定：RRSIG 的 labels 小于所有者名标签数（不计根，*. 不计入）时视为通配符展开，须用 *.<最近包围者> 重建签名名字验签，并附 NSEC 证明下一更近名不存在；labels 大于标签数为伪造；labels 等于标签数时所有者名不得以 * 开头。测多标签展开、缺证明和越界 labels。

## 实现

纯标准库的可导入包 `netsec`（module `example.org/netsec/q0338`）。

- `Classify(owner, sigLabels)`：执行三分支标签判定，返回是否通配符展开、
  最近包围者以及重建后的签名名字 `*.<closest encloser>`。
  - `Labels > 所有者标签数` → `ErrLabelsExceedOwner`（伪造）。
  - `Labels == 所有者标签数` → 精确匹配，所有者名不得以 `*` 开头
    （否则 `ErrWildcardOnExactName`）。
  - `Labels < 所有者标签数` → 通配符展开，最近包围者取 `Labels-1` 个标签。
- `VerifyNSECProof(...)`：校验存在最近包围者 NSEC，且其规范序区间覆盖
  下一更近名（即 `*.<closest encloser>`），缺证明返回 `ErrProofMissing`。
- `Verify(...)`：在判定之上叠加 NSEC 校验、有效期、DNSKEY 匹配与
  RFC 4034 规范 RRSIG 密码学验签（RSA-SHA256/SHA512、ECDSA P-256/P-384、
  Ed25519）。

标签计数不含根标签；所有者名前导的 `*` 标签不计入所有者标签数，而 RRSIG 的
`Labels` 字段仍计入通配符的 `*`。

## 测试

```sh
go test ./...
go test -race -cover ./...
```

覆盖多标签展开（`*.sub.example.com` → `a.b.sub.example.com`）、单标签展开、
缺失/空 NSEC 证明、无包围者 NSEC、越界 `Labels` 伪造、等标签带 `*`、
签名篡改与过期等场景。
