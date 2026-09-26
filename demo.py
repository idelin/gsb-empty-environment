"""Generate synthetic UTF-8 documents and run the similarity pipeline.

The fixture deliberately contains a transitivity trap:
  alpha.txt ~ beta.txt (shared first half), beta.txt ~ gamma.txt (shared
  second half), but alpha.txt vs gamma.txt share almost nothing.
The report must show edges alpha-beta and beta-gamma, one cluster
{alpha, beta, gamma}, and NO alpha-gamma edge.
"""

from pathlib import Path

from simcluster import build_report, find_candidates, load_documents, render_text, verify_candidates

DOCS_DIR = Path("sample_docs")

DOCS = {
    "alpha.txt": (
        "the quick brown fox jumps over the lazy dog near the river bank "
        "morning light falls softly on quiet water while birds sing"
    ),
    "beta.txt": (
        "the quick brown fox jumps over the lazy dog near the river bank "
        "evening traffic moves slowly through crowded city streets at night"
    ),
    "gamma.txt": (
        "fresh bread smells wonderful in the small bakery on the corner "
        "evening traffic moves slowly through crowded city streets at night"
    ),
    "delta.txt": (
        "量子计算 正在 改变 密码学 与 材料 科学 的 研究 方式 "
        "量子计算 正在 改变 密码学 与 材料 科学 的 研究 方式"
    ),
    "epsilon.txt": (
        "量子计算 正在 改变 密码学 与 材料 科学 的 研究 方式 "
        "新的 算法 需要 更 稳定 的 量子 比特"
    ),
    "short.txt": "tiny doc",
    "empty.txt": "",
}


def main() -> None:
    DOCS_DIR.mkdir(exist_ok=True)
    for name, text in DOCS.items():
        (DOCS_DIR / name).write_text(text, encoding="utf-8")

    k, threshold = 3, 0.2
    paths = sorted(DOCS_DIR.glob("*.txt"))
    docs = load_documents(paths, k)
    candidates = find_candidates(docs)
    edges = verify_candidates(docs, candidates, threshold)
    report = build_report(docs, edges, k, threshold)
    print(render_text(report))


if __name__ == "__main__":
    main()
