"""Demo: compile and run sample match programs, printing diagnostics.

Run with:  python3.11 main.py
"""

from pmatch import MatchError, compile_source, run_source

SAMPLES = [
    ("tagged union + bindings",
     """\
union Shape = Circle(r) | Rect(w, h) | Point
match Rect(3, 4):
  | Point -> 0
  | Circle(r) -> 3 * r * r
  | Rect(w, h) -> w * h
"""),
    ("tuples + literals + guard + len()",
     """\
match (1, "hi"):
  | (0, _) -> "zero"
  | (1, s) if len(s) > 5 -> "long"
  | (1, s) -> "one:" + s
  | _ -> "other"
"""),
    ("nested constructor patterns",
     """\
union Tree = Leaf(v) | Node(l, r)
match Node(Leaf(1), Node(Leaf(2), Leaf(3))):
  | Node(Leaf(a), Node(Leaf(b), Leaf(c))) -> a + b + c
  | _ -> 0
"""),
    ("wildcard first => later case unreachable",
     """\
union U = A | B
match A:
  | _ -> 1
  | A -> 2
"""),
    ("missing tag => non-exhaustive, with witness",
     """\
union U = A | B | C
match A:
  | A -> 1
  | B -> 2
"""),
    ("guarded tag branch does not cover the tag",
     """\
union U = A | B
match B:
  | A if true -> 1
  | B -> 2
"""),
    ("duplicate binding in nested pattern => error",
     """\
match (1, (2,)):
  | (x, (x,)) -> x
  | _ -> 0
"""),
    ("non-exhaustive at run time => MatchError",
     """\
union U = A | B
match B:
  | A -> 1
"""),
    ("UTF-8 byte spans (multibyte comment before the error)",
     """\
# 注释：多字节字符
match (1, 2):
  | (x, x) -> x
"""),
]


def main():
    for title, src in SAMPLES:
        print("=" * 68)
        print(f"### {title}")
        print(src, end="")
        print("-" * 68)
        try:
            value, diags = run_source(src)
        except MatchError as e:
            _, _, diags = compile_source(src)
            value = None
            print(f"runtime MatchError: {e}")
        raw = src.encode("utf-8")
        for d in diags:
            frag = raw[d.span[0]:d.span[1]].decode("utf-8")
            print(f"  {d}  <- {frag!r}")
        if value is not None:
            print(f"result: {value!r}")
        print()


if __name__ == "__main__":
    main()
