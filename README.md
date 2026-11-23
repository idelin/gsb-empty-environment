# 四字节末轮故障候选密钥恢复包

Go 可导入包 `keyrecover`（模块 `prescreen.local/ps-0a938757dd`），只处理本题的
四字节末轮模型，用于从故障前后输出判断末轮轮密钥是否被唯一确定。

## 模型

- 域运算：GF(256)，不可约多项式 `0x11b`（归约低 8 位 `0x1b`）。
- S 盒：先取 GF(256) 乘法逆元（`0` 映射 `0`），再做
  `y XOR rotl8(y,1) XOR rotl8(y,2) XOR rotl8(y,3) XOR rotl8(y,4) XOR 0x63`。
- 正常输出 `C_i = S(v_i) XOR K_i`；故障输出
  `F_i = S(v_i XOR M[i,j]*d) XOR K_i`。
- 未知故障列 `j ∈ [0,3]`、非零差分 `d ∈ [1,255]`；每组观测的 `v,j,d`
  可不同，四字节密钥 `K` 固定。
- `M` 四行依次为 `[2,3,1,1]`、`[1,2,3,1]`、`[1,1,2,3]`、`[3,1,1,2]`。
- 输入最多 8 组 `(C,F)`，输出全部满足“每组存在某个 `(j,d)`”的四字节 `K`。

## 算法（不枚举 2^32）

1. 预计算 S 盒、逆 S 盒与逆 S 盒差分表
   `invDiffTable[alpha][beta] = { x : S(x) XOR S(x XOR alpha) == beta }`。
2. 对每组观测、每个 `(j,d)`，由 `alpha_p = M[p,j]*d` 与输出差分
   `delta_p = C_p XOR F_p` 查表得到可能的 `v_p`，再映射为字节密钥候选
   `K_p = C_p XOR S(v_p)`，组成一个逐字节候选盒并记录 `(j,d)` 见证。
3. 对所有组做弧一致性剪枝（保留“同一盒内四字节联合”的耦合），任何字节
   域变空即判定模型不一致。
4. 在剪枝域上用 MCV + 前向检查的约束回溯枚举可行密钥，按字节序输出，并为
   每个候选附加每组规范 `(j,d)` 见证。搜索节点数与候选数均有上界保护，
   超限返回 `ErrCandidateLimit` 与剪枝域，绝不展开 2^32 全空间。

结论分三态：`StatusInconsistent`（无候选，模型不一致）、`StatusUnique`
（唯一）、`StatusAmbiguous`（多候选，末轮密钥未唯一）。本包只恢复四字节
末轮轮密钥候选，不声称恢复完整密码主密钥。

## 语义约定

- 零故障观测（`C == F`，等价 `d=0`）不含差分信息，按空约束跳过并在
  `Result.SkippedZeroGroups` 中标注；全部为零故障时可行集即宿主给定域，
  域过大时返回 `ErrCandidateLimit` 与全量剪枝域。
- 单字节输出差分在 `M` 的列结构下无法由任何非零 `(j,d)` 产生，判为不一致。

## 主要 API

- `Recover(observations, ...Option) (*Result, error)`：候选恢复入口。
- `WithDomains([4][]byte)`：限定每个密钥字节的允许值，默认全字节域；
  显式传空切片返回 `ErrEmptyDomain`。
- `WithMaxCandidates` / `WithMaxNodes`：候选数与搜索节点预算。
- `Simulate(key, v, j, d)`：用已知本地密钥合成观测，供回归测试。
- `Explains(obs, key, j, d)`：独立校验某 `(j,d)` 是否能解释一组观测。
- `SBox` / `InvSBox` / `MixColumns`：基础原语访问。

## 测试

`go test ./...` 覆盖：GF/S 盒标准向量与差分表自洽、已知本地 K 合成多组
唯一恢复、单字节无效故障判不一致、重复观测不丢解、零故障输入跳过、宿主
小域限制与排除真实密钥、候选/节点上限、候选字节序，以及每字节仅 4 个允许
值的小域上用独立全组合枚举（4^4=256）交叉验证候选集与见证。示例见
`keyrecover/example_test.go`。
