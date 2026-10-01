"""运行样例: python3 samples.py"""
from cfgdoctor import (Grammar, IncrementalAnalyzer, analyze, compute_generating,
                       enumerate_terminal_strings, format_report)


def sample_dead_cycle():
    # 仅 A->B, B->A 不可生成; S 另有终结出口
    return Grammar({"a"}, {"S", "A", "B"}, "S", [
        ("S", ("a",)),
        ("S", ("A",)),
        ("A", ("B",)),
        ("B", ("A",)),
    ])


def sample_nullable_cycle():
    # 可空 epsilon 环另有终结出口: 不能全删
    return Grammar({"a"}, {"S", "A", "B"}, "S", [
        ("S", ("A",)),
        ("A", ("B",)),
        ("A", ("a",)),
        ("B", ("A",)),
        ("B", ()),
    ])


def sample_unreachable():
    # X, Y 可生成但自开始符不可达
    return Grammar({"a", "b"}, {"S", "X", "Y"}, "S", [
        ("S", ("a",)),
        ("X", ("Y",)),
        ("Y", ("X",)),
        ("Y", ("b",)),
    ])


def sample_empty_language():
    # 开始符无用: 返回空语言说明
    return Grammar(set(), {"S", "A", "B"}, "S", [
        ("S", ("A",)),
        ("A", ("B",)),
        ("B", ("A",)),
    ])


def sample_infinite_language():
    # 语言无限(泵送), 但无 epsilon 环引起的无限歧义: 对照组
    return Grammar({"a", "b"}, {"S"}, "S", [
        ("S", ("a", "S")),
        ("S", ("b",)),
    ])


def demo_incremental():
    g = Grammar({"a", "b", "c"}, {"S", "A", "B"}, "S", [
        ("S", ("A", "B")),   # 0
        ("S", ("c",)),       # 1
        ("A", ("a",)),       # 2
        ("A", ("B",)),       # 3
        ("B", ("b",)),       # 4
        ("B", ("A",)),       # 5
    ])
    inc = IncrementalAnalyzer(g)
    print("== 增量修复演示: 规则撤回 ==")
    print(f"初始: 可生成={sorted(inc.generating)} "
          f"可达={sorted(inc.reachable)}")
    for idx in (2, 4, 1):
        cs = inc.retract(idx)
        print(f"撤回 #{idx} [{g.productions[idx]}]: "
              f"失去可生成={sorted(cs.lost_generating)} "
              f"失去可达={sorted(cs.lost_reachable)} "
              f"语言变空={cs.language_emptied}")
    print()


def demo_enum_is_not_proof():
    # 枚举找不到 ≠ 不可生成: 需要 9 步推导才能见到终结串
    nts = {"S"} | {f"A{k}" for k in range(1, 9)}
    prods = ([("S", ("A1",))]
             + [(f"A{k}", (f"A{k+1}",)) for k in range(1, 8)]
             + [("A8", ("a",))])
    g = Grammar({"a"}, nts, "S", prods)
    cert = compute_generating(g)
    shallow = enumerate_terminal_strings(g, "S", 3)
    print("== 枚举找不到 ≠ 不可生成 ==")
    print(f"  界=3 的枚举结果: {sorted(shallow) or '∅(什么都没找到)'}")
    print(f"  但最小不动点证书表明 S 可生成, 见证串 "
          f"{cert.witness_string['S']}, 需要 9 步推导;")
    print("  负向结论只能由不动点证书证明, 不能由有界枚举给出")
    print()


def main():
    for name, fn in [
            ("仅 A->B, B->A 不可生成", sample_dead_cycle),
            ("可空 epsilon 环另有终结出口(不能全删)", sample_nullable_cycle),
            ("不可达符号", sample_unreachable),
            ("开始符无用 → 空语言", sample_empty_language),
            ("语言无限(对照: 非歧义来源)", sample_infinite_language)]:
        print("=" * 64)
        print(f"样例: {name}")
        print("=" * 64)
        print(format_report(analyze(fn())))
        print()
    demo_incremental()
    demo_enum_is_not_proof()


if __name__ == "__main__":
    main()
