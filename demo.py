"""运行样例。

证明目标：d/dx [(x+1)(x+2)] 在 x = 1 处的值为 5。

  步骤 0  expand:        (x+1)(x+2) = x^2 + 2x + x + 2
  步骤 1  combine:       x^2 + 2x + x + 2 = x^2 + 3x + 2
  步骤 2  differentiate: d/dx (x^2+3x+2) = 2x + 3
  步骤 3  substitute:    x = 1 代入得 5

依次演示：完整证明通过、篡改一个系数定位首个失败步骤、未知规则拒绝。
"""
import copy

from proofcheck.verifier import verify

X = {"var": "x"}


def c(value):
    return {"const": str(value)}


def add(*terms):
    return {"add": list(terms)}


def mul(*factors):
    return {"mul": list(factors)}


def pw(base, n):
    return {"pow": [base, n]}


def sample_proof():
    return {
        "variable": "x",
        "steps": [
            {
                "rule": "expand",
                "domain": "x ∈ ℚ（分配律处处成立）",
                "input": mul(add(X, c(1)), add(X, c(2))),
                "output": add(pw(X, 2), mul(c(2), X), X, c(2)),
            },
            {
                "rule": "combine",
                "domain": "x ∈ ℚ（恒等变形处处成立）",
                "input": add(pw(X, 2), mul(c(2), X), X, c(2)),
                "output": add(pw(X, 2), mul(c(3), X), c(2)),
            },
            {
                "rule": "differentiate",
                "domain": "多项式在 ℚ 上处处可导",
                "var": "x",
                "input": add(pw(X, 2), mul(c(3), X), c(2)),
                "output": add(mul(c(2), X), c(3)),
            },
            {
                "rule": "substitute",
                "domain": "x = 1 ∈ ℚ，多项式处处有定义",
                "var": "x",
                "value": "1",
                "input": add(mul(c(2), X), c(3)),
                "output": c(5),
            },
        ],
        "result": c(5),
    }


def show(title, proof):
    print(f"== {title} ==")
    res = verify(proof)
    for step in res.steps:
        print(f"  步骤 {step.index}: {step.rule:<13} 通过（适用域：{step.domain}）")
    if res.ok:
        print("  结论：证明有效，最终结果等于最后验证表达式")
    else:
        where = "全局" if res.failed_step is None else f"步骤 {res.failed_step}"
        print(f"  拒绝：首个失败位置 = {where}；原因：{res.error}")
    print()


def main():
    show("完整证明", sample_proof())

    tampered = copy.deepcopy(sample_proof())
    tampered["steps"][1]["output"]["add"][1]["mul"][0] = c(4)  # 3x 改成 4x
    show("篡改：合并步骤把系数 3 改成 4", tampered)

    unknown = copy.deepcopy(sample_proof())
    unknown["steps"][2]["rule"] = "simplify"
    show("未知规则 simplify", unknown)


if __name__ == "__main__":
    main()
