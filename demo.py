"""Runnable sample of the multi-layer position mapping system.

    python3.11 demo.py
"""
from posmap.pipeline import Pipeline


def show_diagnosis(title, p, diag):
    ver = p.versions[diag.vid]
    d = p.explain(diag)
    print(f"\n--- {title} ---")
    print(f"runtime error at pc={diag.pc} (version {diag.vid}): {diag.message}")
    print(f"instruction: {ver.instrs[diag.pc].op} {ver.instrs[diag.pc].arg or ''}")
    print("all candidates from the reverse lookup:")
    for c in d.candidates:
        loc = f"line {c.line} {c.snippet!r}" if c.user_span else "(no source)"
        extra = f" [macro '{c.macro}', definition attached]" if c.macro else ""
        print(f"  - {c.tag:<10} {loc}{extra}\n      reason: {c.reason}")
    if d.best_span:
        snippet = ver.source.encode("utf-8")[d.best_span.start:d.best_span.end].decode()
        print(f"chosen fragment: line {d.best_line} {snippet!r}")
    else:
        print("chosen fragment: none (generated code, no line fabricated)")
    print(f"selection reason: {d.best_reason}")


def main():
    print("=" * 72)
    print("1. source -> macro expansion -> bytecode (positions are UTF-8 bytes)")
    print("=" * 72)
    src = (
        "# 宏示例：中文注释也按字节定位\n"
        "macro twice(e) { e + e }\n"
        "let a = 3;\n"
        "print(twice(a));\n"
        "print(twice(7));\n"
    )
    p = Pipeline(src)
    ver = p.versions[0]
    print("source:"); print(src)
    print("expanded buffer:", repr(ver.expanded))
    print("bytecode:")
    for pc, ins in enumerate(ver.instrs):
        print(f"  pc={pc:<2} {ins.op:<6} {ins.arg if ins.arg is not None else ''}")
    out, _ = p.run()
    print("run output:", out)

    print()
    print("=" * 72)
    print("2. diagnostic backtrace: bytecode pc -> most appropriate user fragment")
    print("=" * 72)
    src2 = (
        "macro safe_div(x, y) { x / y }\n"
        "let a = 10;\n"
        "let b = 0;\n"
        "print(safe_div(a, b));\n"
    )
    p2 = Pipeline(src2)
    _, diag = p2.run()
    show_diagnosis("division by zero inside a macro expansion", p2, diag)

    print()
    print("=" * 72)
    print("3. generated code never fabricates a line number")
    print("=" * 72)
    halt_pc = len(ver.instrs) - 1
    d = p.explain_raw(0, halt_pc)
    print(f"HALT at pc={halt_pc}: candidates={[(c.tag, c.line) for c in d.candidates]}")
    print(f"best line: {d.best_line}  ({d.best_reason})")

    print()
    print("=" * 72)
    print("4. local recompile after a macro update; old diagnostics stay valid")
    print("=" * 72)
    src3 = "macro d(x) { x / q }\nprint(d(1));\n"
    p3 = Pipeline(src3)
    _, diag0 = p3.run()
    print(f"version 0 error: pc={diag0.pc} {diag0.message!r}")
    report = p3.update_macro("d", "x + 1 + 2 + 3 / q")
    print(f"update: recompiled statements {report.recompiled_units}, "
          f"touched {report.touched_blocks} mapping blocks")
    _, diag1 = p3.run()
    print(f"version 1 error: pc={diag1.pc} {diag1.message!r} "
          f"(bytecode grew to {len(p3.versions[1].instrs)} instructions)")
    old = p3.explain(diag0)
    snippet = p3.versions[0].source.encode("utf-8")[old.best_span.start:old.best_span.end].decode()
    print(f"old diagnostic still resolves: line {old.best_line} {snippet!r} (version 0 source)")
    try:
        p3.explain_raw(0, diag1.pc)
    except ValueError as exc:
        print(f"cross-version guard: {exc}")


if __name__ == "__main__":
    main()
