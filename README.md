# NSEC3 证明核验包（Go）

`nsec3/` 是一个可导入的 Go 包，用于递归解析器验证 NSEC3（RFC 5155）否定应答与
通配符扩展证明。仅依赖 Go 标准库。

## 能力

- 域名规范化：小写、无压缩线格式编码（RFC 4034 §6.2）。
- 加盐迭代 SHA-1（RFC 5155 §5）：
  `H(0) = SHA1(wire_name || salt)`，随后执行 `iterations` 次
  `H(k) = SHA1(H(k-1) || salt)`；`HashNameChain` 额外暴露全部中间值。
- Base32hex（RFC 4648 §7 扩展十六进制字母表）无填充编解码，兼容大小写输入。
- 环形哈希区间覆盖（RFC 5155 §3.3）：区分单记录全覆盖（`owner == next`）、
  普通区间（`owner < x < next`）与跨零回绕（`owner > next` 且
  `x > owner || x < next`），端点相等永不视为覆盖。
- RFC 4034 §4.1.2 窗口块型类型位图解析与规范化编码。
- 四类证明核验（入口均在 `Verifier` 上）：
  - `VerifyNXDOMAIN`：最近包围者精确匹配 + Next Closer 覆盖 +
    `*.<CE>` 覆盖三元组（RFC 5155 §8.3）。
  - `VerifyNODATA`：QNAME 精确匹配且位图不含 qtype/CNAME（DS 查询还排除 SOA）、
    空非终端（ENT）覆盖路径、以及仅适用于 DS 的 Opt-Out 委派路径（§8.4）。
  - `VerifyUnsignedDelegation`：Next Closer 覆盖记录必须置位 Opt-Out，
    且最近包围者本身不是委派点（有 NS 无 SOA）。
  - `VerifyWildcardAnswer`：核验 Next Closer 被覆盖，防止更具体的名称屏蔽通配符。
- 安全拒绝：
  - 未知哈希算法（仅接受算法 1 / SHA-1）；
  - 迭代次数超过 `MaxIterations`（100，遵循 RFC 9276）；
  - 跨区记录（算法/迭代/盐不一致）与重复 owner；
  - 未置位 Opt-Out 时用覆盖记录冒充无签名委派。

## 快速上手

```go
import "prescreen.local/ps-1eac5e7124/nsec3"

hash, err := nsec3.HashName(nsec3.HashSHA1, 12, salt, "a.example.")
label := nsec3.Base32HexEncode(hash) // 35mthgpgcu1qg68fab165klnsnk3dpvl

v, err := nsec3.NewVerifier(records)          // 同一链上的 NSEC3 记录
proof, err := v.VerifyNXDOMAIN("x.y.example.")
```

更多可运行示例见 `nsec3/example_test.go`。

## 测试

```
go test ./...
go vet ./...
```

测试覆盖 RFC 5155 附录 A 标准哈希向量、环形回绕边界、空非终端 NODATA、
Opt-Out 合法与伪造拦截、通配符 Next Closer 证明，以及线格式/位图往返与各类拒绝路径。
