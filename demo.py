import json

import pure_expr as pe


SAMPLES = [
    ("arithmetic folding", "1 + 2 * 3", {}),
    ("false short-circuits division", "false && (1 / 0)", {}),
    ("true short-circuits division", "true || (1 / 0)", {}),
    ("constant conditional", "false ? 1 / 0 : 40 + 2", {}),
    ("common subexpression", "(x + 1) + (x + 1)", {"x": 40}),
    ("erroring common expression", "(x / 0) + (x / 0)", {"x": 1}),
    ("utf-8 positions", "值 + 1", {"值": 41}),
]


def contract(source, optimized_ast, variables):
    try:
        before = pe.evaluate(pe.parse(source), variables)
        before_contract = {"result": before}
    except pe.EvalError as exc:
        before_contract = {"error": exc.kind, "span": list(exc.span)}
    try:
        after = pe.evaluate(optimized_ast, variables)
        after_contract = {"result": after}
    except pe.EvalError as exc:
        after_contract = {"error": exc.kind, "span": list(exc.span)}
    return {
        "before": before_contract,
        "after": after_contract,
        "matches": before_contract == after_contract,
    }


def main():
    output = []
    for title, source, variables in SAMPLES:
        result = pe.optimize(source)
        output.append({
            "title": title,
            "source": source,
            "variables": variables,
            "optimized_ir": result["optimized"],
            "contract": contract(source, result["optimized_ast"], variables),
            "reason_mapping": result["reasons"],
        })
    print(json.dumps(output, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
