"""Replayable end-to-end samples for constkit.

Run:  python3.11 demo.py
Each section is deterministic; the checkpoint section uses a temp directory.
"""
import os
import tempfile

from constkit import CancelToken, Cancelled, Engine, EnumVal


def header(title):
    print(f"\n{'=' * 72}\n{title}\n{'=' * 72}")


def show_diags(diags):
    if not diags:
        print("  diagnostics: (none)")
    for d in diags:
        print("  " + d.format().replace("\n", "\n  "))


def main():
    # 1. forward reference --------------------------------------------------
    header("1. Forward reference: A = B + 1, B = 2  =>  A = 3")
    e = Engine()
    src_ab = ("module M;\n"
              "const A = B + 1;\n"
              "when !fast { const B = 2; }\n"
              "when fast  { const B = A; }\n")
    e.apply(sources={"M": src_ab})
    print("  A =", e.artifact("M", "A"), " B =", e.artifact("M", "B"))

    # 2. condition switch creates a new cycle -------------------------------
    header("2. Switch 'fast' on: B = A introduces a new cycle")
    r = e.apply(flags={"fast": True})
    show_diags(r.diagnostics)
    print("  last-good artifacts are NOT polluted: A =", e.artifact("M", "A"),
          " B =", e.artifact("M", "B"))

    # 3. switch back revokes the diagnostics --------------------------------
    header("3. Switch 'fast' back off: diagnostics revoked")
    r = e.apply(flags={"fast": False})
    show_diags(r.diagnostics)
    print("  A =", e.artifact("M", "A"), " B =", e.artifact("M", "B"))

    # 4. same-named constants across modules + local/export separation ------
    header("4. Same-named constants do not misbind; locals are not exported")
    e2 = Engine()
    r = e2.apply(sources={
        "M": "module M;\nlocal secret = 100;\nconst pub = secret + 1;\n",
        "N": "module N;\nconst secret = 2;\n"
             "const ok = M.pub + secret;\nconst bad = M.secret;\n",
        "A": "module A;\nconst v = 1;\nconst r = v + Bmod.v;\n",
        "Bmod": "module Bmod;\nconst v = 2;\n",
    })
    print("  M.pub =", e2.artifact("M", "pub"),
          " N.ok =", e2.artifact("N", "ok"),
          " A.r =", e2.artifact("A", "r"))
    show_diags(r.diagnostics)

    # 5. enums and array indexing -------------------------------------------
    header("5. Enums, arrays and indexed references")
    e3 = Engine()
    e3.apply(sources={"M": "module M;\n"
                           "enum Color { Red, Green = 5, Blue }\n"
                           "const a = [10, 20, 30];\nconst i = 2;\n"
                           "const c = Color.Blue;\nconst x = a[i] + Color.Green;\n"})
    print("  Color.Blue =", e3.artifact("M", "c"))
    print("  a[2] + Color.Green =", e3.artifact("M", "x"))

    # 6. batch: validate before publish --------------------------------------
    header("6. Batch modify: validate first, publish only when clean")
    e4 = Engine()
    e4.apply(sources={"M": "module M;\nconst A = 1;\nconst B = A + 1;\n"})
    print("  before: B =", e4.artifact("M", "B"))
    bad = "module M;\nconst A = 1;\nconst Z = A - A;\nconst B = 1 / Z;\n"
    r = e4.apply(sources={"M": bad}, require_clean=True)
    print("  rejected batch committed?", r.committed)
    show_diags(r.diagnostics)
    print("  published B still =", e4.artifact("M", "B"))
    fixed = "module M;\nconst A = 41;\nconst B = A + 1;\n"
    r = e4.apply(sources={"M": fixed}, require_clean=True)
    print("  fixed batch committed?", r.committed, " B =", e4.artifact("M", "B"))

    # 7. incremental counter proves unrelated modules are not evaluated -----
    header("7. Dependency fingerprint: editing M never evaluates N")
    e5 = Engine()
    e5.apply(sources={
        "M": "module M;\nconst a = 1;\nconst b = a + 1;\n",
        "N": "module N;\nconst x = 10;\nconst y = x * 2;\n",
        "P": "module P;\nconst q = M.b + 1;\n",
    })
    print("  initial eval_count =", e5.eval_count)
    e5.eval_count = 0
    e5.apply(sources={"M": "module M;\nconst a = 2;\nconst b = a + 1;\n"})
    print("  after editing M: evaluated =", e5.last_evaluated)
    print("                    reused    =", e5.last_reused)

    # 8. integer overflow policies -------------------------------------------
    header("8. Integer overflow policies (signed 64-bit)")
    top = 9223372036854775807
    for mode in ("exact", "wrap", "error"):
        em = Engine(overflow=mode)
        em.apply(sources={"M": f"module M;\nconst X = {top} + 1;\n"})
        if any(d.code == "overflow" for d in em.diagnostics):
            print(f"  {mode:5}: overflow diagnostic (artifact unavailable / last-good kept)")
        else:
            print(f"  {mode:5}: X =", em.artifact("M", "X"))

    # 9. cancellation and checkpoint recovery consistency --------------------
    header("9. Cancellation leaves state intact; checkpoint restores exactly")
    e6 = Engine()
    e6.apply(sources={"M": src_ab, "N": "module N;\nconst x = 42;\n"})
    token = CancelToken()
    token.cancel()
    try:
        e6.apply(sources={"M": "module M;\nconst A = 999;\n"}, token=token)
    except Cancelled:
        print("  cancelled apply raised Cancelled; A still =", e6.artifact("M", "A"))
    with tempfile.TemporaryDirectory() as tmp:
        path = os.path.join(tmp, "checkpoint.json")
        e6.save_checkpoint(path)
        restored = Engine.load_checkpoint(path)
        print("  artifacts after restore equal engine state:",
              restored.artifacts() == e6.artifacts())
        change = "module M;\nconst A = 70;\nconst B = 71;\n"
        r1 = e6.apply(sources={"M": change})
        r2 = restored.apply(sources={"M": change})
        print("  post-restore evaluated sets identical:", r1.evaluated == r2.evaluated,
              "->", r2.evaluated)
        print("  post-restore artifacts identical:",
              restored.artifacts() == e6.artifacts())


if __name__ == "__main__":
    main()
