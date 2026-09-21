# -*- coding: utf-8 -*-
"""运行样例:全量解析、增量复用、错误恢复与解释执行。

    python3 demo.py
"""

from minilang import (Document, EvalError, FuncDecl, LetDecl,
                      compute_positions, evaluate, parse, parse_text,
                      tree_signature)

SOURCE = (
    "# 演示:圆面积与翻倍(注释含中文,验证 UTF-8 字节偏移)\n"
    "def area(r) = 3.14 * r * r;\n"
    "def double(x) = x + x;\n"
    "let a = area(2);\n"
    "let b = double(a);\n"
)


def decl_label(decl):
    if isinstance(decl, FuncDecl):
        return f"def {decl.name}"
    if isinstance(decl, LetDecl):
        return f"let {decl.name}"
    return type(decl).__name__


def main():
    tree, errors = parse(SOURCE)
    positions = compute_positions(tree)
    print("== 1. 全量解析(位置为 UTF-8 字节区间)==")
    for decl in tree.decls:
        start, end = positions[id(decl)]
        print(f"  {decl_label(decl):<12} 字节 [{start:>3}, {end:>3})")
    print("  解释执行:", evaluate(tree))
    print()

    doc = Document(SOURCE)
    old_decls = list(doc.tree.decls)
    old_positions = doc.positions
    start = SOURCE.encode("utf-8").index(b"3.14")
    doc.apply_edit(start, start + 4, "3.14159")  # 在 area 函数体内改数字
    print("== 2. 增量编辑:area 函数体内 3.14 -> 3.14159 ==")
    for old, new in zip(old_decls, doc.tree.decls):
        reused = "复用(同一对象)" if old is new else "重新解析"
        new_start, new_end = doc.positions[id(new)]
        old_start = old_positions[id(old)][0]
        print(f"  {decl_label(new):<12} {reused:<14} 字节 [{new_start:>3}, {new_end:>3})"
              f"(原起点 {old_start})")
    print("  解释执行:", evaluate(doc.tree))
    full_tree, _, _ = parse_text(doc.text)
    same = tree_signature(doc.tree) == tree_signature(full_tree)
    print("  增量树与全量树语义一致:", same)
    print()

    bad_src = "def bad() = (1 + 2;\ndef good() = 42;\nlet g = good();\n"
    bad_tree, bad_errors = parse(bad_src)
    print("== 3. 错误恢复:未闭合括号 ==")
    for message, err_start, err_end in bad_errors:
        print(f"  错误: {message} (字节 {err_start}..{err_end})")
    print("  后续声明仍可解析:", [decl_label(d) for d in bad_tree.decls])
    try:
        evaluate(bad_tree)
    except EvalError as exc:
        print("  解释执行:", exc)


if __name__ == "__main__":
    main()
