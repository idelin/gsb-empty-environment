"""Run the checker over the sample models and print the evidence."""

import samples
from checker import check
from model import out_edges


def format_trace(prog, trace) -> str:
    lines = []
    for i, name in enumerate(trace):
        node = prog.nodes[name]
        note = ""
        if i:
            prev = prog.nodes[trace[i - 1]]
            kinds = [e.kind for e in out_edges(prev) if e.target == name]
            note = f"   [{kinds[0]}]" if kinds else ""
        lines.append(f"      {name}: {node.op}{note}")
    return "\n".join(lines)


def main() -> None:
    for build in samples.ALL:
        prog = build()
        rep = check(prog)
        print(f"== {rep.program} ==  verdict: {rep.verdict.upper()}")
        for f in rep.findings:
            print(f"  [{f.certainty}] {f.kind}: resource '{f.res}' at node '{f.node}'")
            if f.trace:
                print("    witness (replayable):")
                print(format_trace(prog, f.trace))
        for c in rep.filtered:
            print(f"  [filtered false positive] {c.kind}: '{c.res}' at "
                  f"'{c.node}' (exhaustive replay found no witness)")
        for p in rep.pending_reasons:
            print(f"  [pending] {p}")
        print(f"  stats: {rep.stats}")
        print()


if __name__ == "__main__":
    main()
