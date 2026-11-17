# 通配符展开与RRSIG标签数

攻击者让解析器把通配符展开的应答当成具体名字存在。为Go验证包写可导入的 RRSIG 通配符判定：RRSIG 的 labels 小于所有者名标签数（不计根，*. 不计入）时视为通配符展开，须用 *.<最近包围者> 重建签名名字验签，并附 NSEC 证明下一更近名不存在；labels 大于标签数为伪造；labels 等于标签数时所有者名不得以 * 开头。测多标签展开、缺证明和越界 labels。

## 判定规则（RFC 4034 §3.1 / RFC 4035 §5.3.4）

设所有者名标签数为 `n`（不计根标签；字面 `*` 按普通标签计数），RRSIG 的 Labels 字段为 `l`：

- `l > n`：伪造，返回 `ErrLabelsOutOfRange`，状态为 `RRSIGForged`。
- `l == n`：精确签名，状态为 `RRSIGExact`；此时所有者名不得以 `*` 开头，否则返回 `ErrWildcardOwnerOnExact`。
- `l < n`：通配符展开，状态为 `RRSIGWildcardExpanded`：
  - 最近包围者 = 所有者名最右侧 `l` 个标签；
  - 重建签名名字 `SignedName = "*.<最近包围者>"`，调用方须用它（而非展开后的所有者名）验签；
  - 下一更近名 = 最右侧 `l+1` 个标签，必须由 NSEC 证明不存在：
    一条 NSEC 的所有者为最近包围者，另一条 NSEC 以规范排序开区间覆盖下一更近名
    （同一条 NSEC 可兼任两角色）；证明缺失或不成立分别返回
    `ErrMissingNSECProof`、`ErrNSECNoClosestEncloser`、`ErrNSECNoNextCloser`。

## 用法

```go
import "example.org/netsec/q0338"

res, err := netsec.AnalyzeRRSIG(ownerName, rrsig, nsecProof)
switch {
case errors.Is(err, netsec.ErrLabelsOutOfRange):
    // 伪造应答，丢弃
case errors.Is(err, netsec.ErrMissingNSECProof),
     errors.Is(err, netsec.ErrNSECNoClosestEncloser),
     errors.Is(err, netsec.ErrNSECNoNextCloser):
    // 通配符展开缺少有效的不存在证明，丢弃
case res.Status == netsec.RRSIGWildcardExpanded:
    // 用 res.SignedName（如 "*.example."）重建签名输入并验签
case res.Status == netsec.RRSIGExact:
    // 直接用所有者名验签
}
```

辅助 API：

- `netsec.LabelCount(name)`：绝对域名的标签数（非法名返回 -1）。
- `netsec.CanonicallyCovers(nsec, name)`：按 RFC 4034 §4.1.1/§6.1 判断 NSEC 开区间（含 zone apex 回绕）是否覆盖某名字。

测试：`go test ./...`（覆盖多标签展开、缺证明、越界 labels 及 NSEC 区间边界/回绕）。
