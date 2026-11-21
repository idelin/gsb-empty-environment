为域名校验器实现否定应答证明组件：按区间覆盖关系验证被否定名称确实落在空档内，核对通配符否定的展开顺序，拒绝区间颠倒或未覆盖目标的证明。用一条伪造空档的应答验证拒绝并指出未覆盖区间。

## 实现

包 `nsecproof`（根目录，module `prescreen.local/ps-ea3efc9c72`）按 RFC 4034/4035
的 NSEC 模型验证否定应答证明：

- `nsec.go`：规范化域名与规范排序（RFC 4034 §6.1）、`NSEC.Validate(zone)`
  区间方向校验、`NSEC.Covers(target, zone)` 区间覆盖（含回到区顶点的回绕记录）。
- `proof.go`：`Proof.Verify()` 执行完整校验——
  1. 拒绝颠倒区间（owner 不小于 NextDomain 且未回绕到区顶点，`ErrReversedInterval`）；
  2. 求最近闭合点（closest encloser），要求其下一级名称被某条 NSEC 空档覆盖，
     否则返回 `CoverageError`（内嵌 `ErrUncoveredTarget`）并给出未覆盖区间；
  3. 通配符否定只允许 `*.<最近闭合点>` 展开，层级不符返回 `ErrWildcardMismatch`；
     通配符存在时 NXDOMAIN 矛盾（`ErrWildcardExists`）；
  4. NODATA 还要求否认方 NSEC 的类型位图不含 qtype（`ErrQtypePresent`）。

## 伪造空档拒绝示例

`TestForgedGapRejected` / `ExampleProof_Verify_forgedGap` 构造一条伪造应答：
NSEC 区间 `(c.example., m.example.)` 声称覆盖 `bbb.example.`，但按规范排序
双标签 `bbb` 排在所有单标签名之后，目标并未落入任何空档。校验被拒绝并指出
未覆盖区间 `(m.example., bbb.example.)`。

## 运行

```sh
go test ./... -v
```
