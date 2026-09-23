"""运行样例：python3 demo.py"""
from fractions import Fraction

from sturm_iso import (NOTES, REVISION_NOTES, isolate, revise_isolation,
                       revise_polynomial, verify_isolation)
from sturm_iso.polynomials import mul, poly, poly_to_str


def show(result, title):
    print(f"=== {title} ===")
    print(f"p(x) = {poly_to_str(result.polynomial)}")
    print(f"无平方部分 q(x) = {poly_to_str(result.squarefree_part)}")
    factors = ", ".join(f"({poly_to_str(f)})^{m}"
                        for f, m in result.squarefree_factors) or "1"
    print(f"无平方分解 p = {factors}")
    print(f"不同实根总数(Sturm 证明): {result.total_distinct_roots}  "
          f"完成: {result.completed}")
    if not result.roots and result.completed:
        print("  (无实根，返回空)")
    for c in result.roots:
        if c.kind == "point":
            print(f"  [点根 {c.root_id}] x = {c.point}  重数 = {c.multiplicity}  "
                  f"所属无平方因子 = {poly_to_str(c.squarefree_factor)}")
        else:
            iv = c.interval
            right = ")" if iv.hi_excluded else "]"
            adjust = "-1(端点恰根)" if iv.hi_excluded else ""
            print(f"  [区间根 {c.root_id}] x in ({iv.lo}, {iv.hi}{right}  "
                  f"重数 = {c.multiplicity}  "
                  f"因子 = {poly_to_str(c.squarefree_factor)}")
            print(f"      证书: V(lo)={c.variations_lo} 符号{c.signs_lo}  "
                  f"V(hi)={c.variations_hi} 符号{c.signs_hi}  "
                  f"根数 = V(lo)-V(hi){adjust} = {c.root_count}")
    for pend in result.pending:
        iv = pend.interval
        print(f"  [未决区间] ({iv.lo}, {iv.hi}]  根数 = {pend.root_count}  "
              f"(V(lo)={pend.variations_lo}, V(hi)={pend.variations_hi})")
    problems = verify_isolation(result)
    print("独立验证器: " + ("通过" if not problems else f"失败 {problems}"))
    print()


def show_revision(rev, title):
    print(f"=== {title} ===")
    print(f"旧 p(x) = {poly_to_str(rev.old_polynomial)}")
    print(f"新 p(x) = {poly_to_str(rev.new_polynomial)}")
    show(rev.isolation, title + " —— 新多项式隔离结果")
    print(f"重用区间数: {rev.reused_intervals}")
    print("身份匹配:")
    for link in rev.links:
        print(f"  [{link.relation}] 旧{link.old_ids} -> 新{link.new_ids}")
        print(f"      {link.detail}")
    print()


def main():
    print("变换步骤适用域说明（每步附带于结果 notes）:")
    for n in NOTES:
        print("  - " + n)
    for n in REVISION_NOTES:
        print("  - " + n)
    print()

    show(isolate(poly([0, 0, 1])), "示例 1: x^2 —— 只有根 0，重数另列")
    show(isolate(poly([1, 0, 1])), "示例 2: x^2 + 1 —— 无实根返回空")

    p3 = mul(mul(poly([-1, 1]), poly([-2, 1])), poly([-3, 1]))
    show(isolate(p3), "示例 3: (x-1)(x-2)(x-3)")

    p4 = mul(mul(poly([0, 1]), poly([-1, 1])), poly([-2, 1]))
    show(isolate(p4), "示例 4: x(x-1)(x-2) —— 端点恰根的精确处理")

    p5 = mul(mul(poly([-1, 1]), poly([-1, 1])), poly([-2, 1]))
    show(isolate(p5), "示例 5: (x-1)^2 (x-2) —— 重根重数另列")

    p6 = mul(mul(poly([-1, 1]), poly([-2, 1])),
             mul(poly([-3, 1]), poly([-4, 1])))
    show(isolate(p6, max_steps=1), "示例 6: 预算中断(max_steps=1)，返回已隔离+未决")

    base = poly([-2, 0, 1])              # x^2 - 2
    old = isolate(mul(base, base))       # (x^2-2)^2，两个二重根
    new_p = revise_polynomial(old.polynomial, {0: Fraction(399, 100)})
    rev = revise_isolation(old, new_p)   # 每个二重根分裂为两个单根
    show_revision(rev, "示例 7: (x^2-2)^2 -> (x^2-2)^2 - 1/100 —— 重根分裂")

    rev_back = revise_isolation(rev.isolation, old.polynomial)
    show_revision(rev_back, "示例 8: 逆向修订 —— 根合并(候选标注，不猜唯一身份)")

    old2 = isolate(base)                 # x^2 - 2
    new2 = revise_polynomial(old2.polynomial, {0: Fraction(-199, 100)})
    rev2 = revise_isolation(old2, new2)  # 小扰动，身份保持
    show_revision(rev2, "示例 9: x^2-2 -> x^2-199/100 —— 区间重用，身份保持")


if __name__ == "__main__":
    main()
