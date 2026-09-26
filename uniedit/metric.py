"""度量性质校验：判定 BK 树剪枝对给定成本模型与字母表是否可证明安全。

BK 树剪枝规则（证明见 README）依赖距离的三角不等式与对称性：
对节点 p、查询 q、槽位 w = d(p, x) 的子树中任意 x，有
    d(q, x) >= |d(q, p) - w|
故槽位 w 落在 [d(q,p)-t, d(q,p)+t] 之外时整棵子树可安全剪掉。

注意：任意权重下，即使不带换位，DP 距离也可能违反三角不等式。
反例：ins(c)=3, ins(b)=1, sub(b,c)=1 时
    d('', 'c')=3  但  d('', 'b')+d('b', 'c')=1+1=2
（脚本"插入 b 再替换为 c"比直接插入 c 便宜，DP 的对齐结构表达不了
"插入后再改"——只有符号级成本满足三角不等式时，DP 才等于脚本最小成本。）

本模块在有效字母表 Σ（词典字符 ∪ 查询字符）上验证以下**有限充分条件**，
满足时（无换位/换位被支配的）DP 距离在 Σ* 上是（伪）度量：

记 sub̂(a,b) = min(sub(a,b), del(a)+ins(b))（DP 对 sub 超过 del+ins 的
部分天然不变：两点间 del+ins 路径已在矩阵中，故可无损吸收）。

1. 非负性：所有相关成本 >= 0。
2. 对称性：ins(c) == del(c)；sub、trans 对称。
3. 符号级三角不等式（吸收后）：
   - sub̂(a,c) <= sub̂(a,b) + sub̂(b,c)   对所有 a,b,c in Σ
   - ins(b) <= ins(a) + sub̂(a,b)       对所有 a,b in Σ（含 del 的对偶）
4. 换位被支配：trans(a,b) >= D_0(a+b, b+a)（无换位 DP 的两字符距离）。

定理 T1（条件 1-3 => 无换位 DP 是伪度量）：
符号成本满足三角不等式时，任何"同一位置编辑多次"的脚本都可替换为
直接边而不更贵，故脚本最小成本 = 对齐最小成本 = DP 值。脚本可顺序
拼接，故 d(x,z) <= d(x,y)+d(y,z)；对称性由条件 2 保证。

引理 L1（条件 4 => 换位项无效）：对 |x|+|y| 归纳。换位项
T = D_t(x[:-2],y[:-2]) + trans(a1,a2)
  >= D_0(x[:-2],y[:-2]) + D_0(a1a2,a2a1) >= D_0(x,y)
（末步为对齐拼接给出的超加性，对 DP 值直接成立）。故带换位 DP 与
无换位 DP 处处相等，度量性质随之成立。

条件不满足时 check_metric 返回 ok=False，索引必须回退到不依赖
三角不等式的安全方案（见 index.py 与 distance.length_lower_bound）。
"""
from __future__ import annotations

from dataclasses import dataclass, field
from typing import Dict, Iterable, List, Tuple

from .costs import CostModel
from .distance import distance


@dataclass
class MetricReport:
    alphabet_size: int
    violations: List[str] = field(default_factory=list)  # 致命：破坏三角不等式/对称性
    warnings: List[str] = field(default_factory=list)  # 非致命：仅破坏正定性

    @property
    def ok(self) -> bool:
        return not self.violations


def check_metric(costs: CostModel, alphabet: Iterable[str]) -> MetricReport:
    """在有限字母表上验证上述充分条件。O(|Σ|^3) 次 O(1) 检查。"""
    chars = sorted(set(alphabet))
    violations: List[str] = []
    warnings: List[str] = []

    for c in chars:
        ci, cd = costs.insert(c), costs.delete(c)
        if ci != cd:
            violations.append(f"insert({c!r})={ci} != delete({c!r})={cd}：距离不对称")
        if ci < 0 or cd < 0:
            violations.append(f"字符 {c!r} 的插入/删除成本为负")
        elif ci == 0:
            warnings.append(f"字符 {c!r} 插入/删除成本为 0：距离不正定（伪度量，剪枝安全）")

    sub_eff: Dict[Tuple[str, str], float] = {}
    for a in chars:
        for b in chars:
            sab = costs.substitute(a, b)
            if sab != costs.substitute(b, a):
                violations.append(f"substitute({a!r},{b!r}) 不对称")
            if sab < 0:
                violations.append(f"substitute({a!r},{b!r}) 为负")
            eff = min(sab, costs.delete(a) + costs.insert(b))
            sub_eff[(a, b)] = eff
            if eff == 0 and a != b:
                warnings.append(f"sub_eff({a!r},{b!r})=0：距离不正定（伪度量，剪枝安全）")

    if violations:
        return MetricReport(len(chars), violations, warnings)

    for a in chars:
        for b in chars:
            if costs.insert(b) > costs.insert(a) + sub_eff[(a, b)]:
                violations.append(
                    f"符号级三角不等式失效：ins({b!r})={costs.insert(b)} > "
                    f"ins({a!r})+sub_eff={costs.insert(a) + sub_eff[(a, b)]}"
                )
            for c in chars:
                if sub_eff[(a, c)] > sub_eff[(a, b)] + sub_eff[(b, c)]:
                    violations.append(
                        f"符号级三角不等式失效：sub_eff({a!r},{c!r})="
                        f"{sub_eff[(a, c)]} > {sub_eff[(a, b)]}+{sub_eff[(b, c)]}"
                    )

    for a in chars:
        for b in chars:
            tab = costs.transpose(a, b)
            if tab != costs.transpose(b, a):
                violations.append(f"transpose({a!r},{b!r}) 不对称")
            if tab < 0:
                violations.append(f"transpose({a!r},{b!r}) 为负")
                continue
            need = distance(a + b, b + a, costs, use_transpose=False)
            if tab < need:
                violations.append(
                    f"transpose({a!r},{b!r})={tab} < 无换位距离 {need}："
                    f"换位未被支配，三角不等式可能失效"
                )

    return MetricReport(len(chars), violations, warnings)
