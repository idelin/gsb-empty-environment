"""Run sample: prints source, diagnostics with UTF-8 byte ranges, minimal
repair candidates, and the interpreter environment after recovery."""

from declang import Document

SOURCE = """\
let width = 640
let height = 480;
let label = "size: 尺寸;
let area = (width + height);
let ratio = width
let deep = ((1 + (2 + 3);
let end = 99;
"""


def show(doc: Document, title: str) -> None:
    data = doc.text.encode("utf-8")
    print(f"== {title} ==")
    for n, diag in enumerate(doc.diagnostics, 1):
        snippet = data[diag.start:diag.end].decode("utf-8", "replace")
        if "recovered by" in diag.message:
            provenance = "minimal repair"
        elif "not a minimal repair" in diag.message:
            provenance = "greedy sync"
        else:
            provenance = "lexical"
        print(f"  {n}. [{diag.severity}] bytes {diag.start}:{diag.end} "
              f"({provenance}) {snippet!r}")
        print(f"     {diag.message}")
        for cand in diag.candidates:
            print(f"       candidate: {cand}")
    print(f"  environment: {doc.env}")
    print()


doc = Document(SOURCE, max_cost=2)
show(doc, "initial source (one pass, multiple independent errors)")

# Local edit: insert the missing ';' after width (byte offset of 'let height').
pos = doc.text.encode("utf-8").index(b"let height")
doc.edit(pos, pos, "   ")  # fill the gap with spaces: no new ';' yet -> still bad
show(doc, "after a local edit that does not fix the missing ';'")

doc.edit(pos, pos, ";")
show(doc, "after inserting the missing ';' (diagnostics updated)")
