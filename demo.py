"""Demo: parse and run a broken source file, showing recovery diagnostics."""
from minilang import run_source

SAMPLE = """\
let width = 8;
let height = 5
let area = width * height;
let label = "area=" + "cm2";
let broken = ((width + 2) * 3;
let tail = "unterminated
let after = 42;
"""


def main():
    result = run_source(SAMPLE)
    print("== source ==")
    print(SAMPLE)
    print("== diagnostics ==")
    for d in result.diagnostics:
        print(d)
        for fix in d.fixes:
            print(f"    fix: {fix}")
    print("== environment after execution ==")
    for name, value in result.env.items():
        print(f"  {name} = {value!r}")


if __name__ == "__main__":
    main()
