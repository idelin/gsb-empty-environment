"""Run sample: search a synthetic UTF-8 document split into indexed segments."""

from normsearch import Index, VersionMismatchError, render_html

RAW = (
    "Die alte STRA\u00dfE wird renoviert. "
    "Im CAF\u00c9 steht ein \ufb01sch\u00adbr\u00f6tchen bereit. "
    "\U0001F600 <script>alert(1)</script>"
)

# The document arrives in chunks; split straight through the word "CAFÉ".
segments = [RAW[:30], RAW[30:45], RAW[45:]]


def main() -> None:
    index = Index()
    for chunk in segments:
        index.add_segment("doc-2026-09-26", chunk)

    print("== original (UTF-16 units:", end=" ")
    print(sum(2 if ord(c) > 0xFFFF else 1 for c in RAW), ") ==")
    print(RAW)
    print()
    print("== normalized view ==")
    print(index.build().normalized)
    print()
    print("== tokens ==")
    for token, lo, hi in index.iter_tokens():
        print(f"  {token!r:14} UTF-16 [{lo}, {hi})")
    print()
    for query in ["strasse", "cafe", "fischbroetchen", "fisch", "script"]:
        ranges = index.search(query)
        print(f"query={query!r:18} ranges={ranges}")
        print("  HTML:", render_html(RAW, ranges))
    print()

    stale = Index()
    stale.add_segment("doc-2026-09-25", "alte Version: CAFÉ")
    try:
        stale.add_segment("doc-2026-09-26", segments[0])
    except VersionMismatchError as exc:
        print("== version guard ==")
        print(" ", exc)


if __name__ == "__main__":
    main()
