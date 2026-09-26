"""Token-shingle Jaccard similarity with exact verification and clustering.

Pipeline
--------
1. Tokenize each UTF-8 document (see ``tokenize`` for the exact rules).
2. Build the set of token shingles (contiguous k-token windows) per document.
3. Generate candidate pairs from an inverted index over *hashed* shingles.
   Hashing is a performance shortcut only: a hash collision can add a
   spurious candidate, but never changes the final decision.
4. Verify every candidate pair by computing the *exact* Jaccard similarity
   on the real shingle sets (tuples of tokens, not hashes). Pairs below the
   threshold are discarded.
5. Cluster documents as connected components over verified edges.

Clustering rule (important)
---------------------------
Similarity is NOT transitive: A~B and B~C does NOT imply A~C.  A cluster
only claims *connectivity* through verified edges; pairwise similarity is
claimed exclusively for the edges listed in the report's evidence section.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import re
import sys
from dataclasses import dataclass, field
from pathlib import Path

# Tokenization rule: casefold (Unicode-aware lowercase), then extract maximal
# runs of Unicode "word" characters (letters, digits, underscore).  Everything
# else (whitespace, punctuation, symbols) is a separator and is dropped.
TOKEN_RE = re.compile(r"\w+", re.UNICODE)

DEFAULT_K = 3
DEFAULT_THRESHOLD = 0.5


def tokenize(text: str) -> list[str]:
    """Split text into tokens: casefold, then keep runs of ``\\w+``."""
    return TOKEN_RE.findall(text.casefold())


def shingle_set(tokens: list[str], k: int) -> set[tuple[str, ...]]:
    """Build the set of contiguous k-token shingles.

    Defined behaviour for short texts:
      * ``len(tokens) >= k``: all contiguous windows of width ``k``.
      * ``0 < len(tokens) < k``: a single shingle equal to the whole token
        sequence (the document is its own only shingle).
      * ``len(tokens) == 0``: the empty set.
    """
    if k < 1:
        raise ValueError("shingle width k must be >= 1")
    if not tokens:
        return set()
    if len(tokens) < k:
        return {tuple(tokens)}
    return {tuple(tokens[i : i + k]) for i in range(len(tokens) - k + 1)}


def jaccard(a: set, b: set) -> float:
    """Exact Jaccard similarity.  Both empty -> 1.0; one empty -> 0.0."""
    if not a and not b:
        return 1.0
    union = a | b
    if not union:
        return 0.0
    return len(a & b) / len(union)


def hash_shingle(shingle: tuple[str, ...]) -> int:
    """Hash a shingle to an int.  Used ONLY for candidate generation."""
    digest = hashlib.blake2b(repr(shingle).encode("utf-8"), digest_size=8)
    return int.from_bytes(digest.digest(), "big")


@dataclass
class Document:
    doc_id: str
    text: str
    tokens: list[str] = field(default_factory=list)
    shingles: set[tuple[str, ...]] = field(default_factory=set)


@dataclass
class Edge:
    """A verified similar pair plus its evidence."""

    doc_a: str
    doc_b: str
    score: float
    shared: int
    union: int
    sample_shared: list[str]


def load_documents(paths: list[Path], k: int) -> list[Document]:
    docs = []
    for path in sorted(paths):
        text = path.read_text(encoding="utf-8")
        tokens = tokenize(text)
        docs.append(
            Document(
                doc_id=path.name,
                text=text,
                tokens=tokens,
                shingles=shingle_set(tokens, k),
            )
        )
    return docs


def find_candidates(
    docs: list[Document], hash_fn=hash_shingle
) -> set[tuple[int, int]]:
    """Candidate pairs = docs sharing at least one *hashed* shingle.

    ``hash_fn`` is injectable so tests can force collisions and prove they
    only affect the candidate set, never the final decision.
    """
    index: dict[int, list[int]] = {}
    for i, doc in enumerate(docs):
        for h in {hash_fn(shingle) for shingle in doc.shingles}:
            index.setdefault(h, []).append(i)
    candidates: set[tuple[int, int]] = set()
    for bucket in index.values():
        for x in range(len(bucket)):
            for y in range(x + 1, len(bucket)):
                candidates.add((bucket[x], bucket[y]))
    return candidates


def verify_candidates(
    docs: list[Document],
    candidates: set[tuple[int, int]],
    threshold: float,
    sample_size: int = 3,
) -> list[Edge]:
    """Exact re-check: compute true Jaccard on real shingle sets."""
    edges = []
    for i, j in sorted(candidates):
        a, b = docs[i].shingles, docs[j].shingles
        score = jaccard(a, b)
        if score < threshold:
            continue
        shared = a & b
        sample = [" ".join(s) for s in sorted(shared)[:sample_size]]
        edges.append(
            Edge(
                doc_a=docs[i].doc_id,
                doc_b=docs[j].doc_id,
                score=score,
                shared=len(shared),
                union=len(a | b),
                sample_shared=sample,
            )
        )
    return edges


def cluster(docs: list[Document], edges: list[Edge]) -> list[list[str]]:
    """Connected components over verified edges (union-find)."""
    parent = {d.doc_id: d.doc_id for d in docs}

    def find(x: str) -> str:
        while parent[x] != x:
            parent[x] = parent[parent[x]]
            x = parent[x]
        return x

    def union(x: str, y: str) -> None:
        rx, ry = find(x), find(y)
        if rx != ry:
            parent[rx] = ry

    for edge in edges:
        union(edge.doc_a, edge.doc_b)

    groups: dict[str, list[str]] = {}
    for doc in docs:
        groups.setdefault(find(doc.doc_id), []).append(doc.doc_id)
    return [sorted(m) for m in sorted(groups.values(), key=lambda m: (m[0], len(m)))]


def build_report(docs: list[Document], edges: list[Edge], k: int, threshold: float) -> dict:
    clusters = cluster(docs, edges)
    edge_keys = {tuple(sorted((e.doc_a, e.doc_b))) for e in edges}
    return {
        "params": {"shingle_width": k, "jaccard_threshold": threshold},
        "documents": [
            {"doc_id": d.doc_id, "tokens": len(d.tokens), "shingles": len(d.shingles)}
            for d in docs
        ],
        "evidence": [
            {
                "pair": [e.doc_a, e.doc_b],
                "jaccard": round(e.score, 6),
                "shared_shingles": e.shared,
                "union_shingles": e.union,
                "sample_shared_shingles": e.sample_shared,
            }
            for e in edges
        ],
        "clusters": [
            {
                "cluster_id": idx,
                "members": members,
                "verified_edges": [
                    [a, b] for a, b in sorted(edge_keys) if a in members and b in members
                ],
            }
            for idx, members in enumerate(clusters)
        ],
        "rule": (
            "Clusters are connected components over verified edges. "
            "Membership claims connectivity only; pairwise similarity is "
            "claimed solely for pairs listed under 'evidence'. "
            "A~B and B~C does NOT imply A~C."
        ),
    }


def render_text(report: dict) -> str:
    lines = []
    p = report["params"]
    lines.append(f"shingle width k={p['shingle_width']}, jaccard threshold={p['jaccard_threshold']}")
    lines.append("")
    lines.append("== Documents ==")
    for d in report["documents"]:
        lines.append(f"  {d['doc_id']}: {d['tokens']} tokens, {d['shingles']} shingles")
    lines.append("")
    lines.append("== Verified similar pairs (evidence) ==")
    if not report["evidence"]:
        lines.append("  (none)")
    for e in report["evidence"]:
        a, b = e["pair"]
        lines.append(
            f"  {a} ~ {b}: jaccard={e['jaccard']:.4f} "
            f"({e['shared_shingles']}/{e['union_shingles']} shingles shared)"
        )
        for s in e["sample_shared_shingles"]:
            lines.append(f'      shared shingle: "{s}"')
    lines.append("")
    lines.append("== Clusters (connected components) ==")
    for c in report["clusters"]:
        lines.append(f"  cluster {c['cluster_id']}: {', '.join(c['members'])}")
        lines.append(f"      verified edges: {c['verified_edges']}")
    lines.append("")
    lines.append(f"Rule: {report['rule']}")
    return "\n".join(lines)


def run(paths: list[Path], k: int, threshold: float) -> dict:
    docs = load_documents(paths, k)
    candidates = find_candidates(docs)
    edges = verify_candidates(docs, candidates, threshold)
    return build_report(docs, edges, k, threshold)


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("docs_dir", type=Path, help="directory of UTF-8 .txt documents")
    parser.add_argument("-k", type=int, default=DEFAULT_K, help="shingle width (default 3)")
    parser.add_argument(
        "-t", "--threshold", type=float, default=DEFAULT_THRESHOLD,
        help="Jaccard threshold in [0, 1] (default 0.5)",
    )
    parser.add_argument("--json", action="store_true", help="emit JSON instead of text")
    args = parser.parse_args(argv)

    if not 0.0 <= args.threshold <= 1.0:
        parser.error("threshold must be in [0, 1]")
    paths = sorted(args.docs_dir.glob("*.txt"))
    if not paths:
        print(f"no .txt files in {args.docs_dir}", file=sys.stderr)
        return 1

    report = run(paths, args.k, args.threshold)
    if args.json:
        print(json.dumps(report, ensure_ascii=False, indent=2))
    else:
        print(render_text(report))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
