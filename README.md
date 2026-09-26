# simcluster — token-shingle Jaccard similarity with exact verification

Pure Python 3.11, standard library only (`re`, `hashlib`, `argparse`, `json`,
`dataclasses`, `pathlib`, `unittest`). No search services, no full-text
search libraries. Documents are local synthetic UTF-8 `.txt` files.

## Pipeline

1. **Tokenize** each document (rules below).
2. **Shingle**: build the set of contiguous k-token windows per document.
3. **Candidates**: inverted index over *hashed* shingles; any pair sharing a
   hashed shingle becomes a candidate.
4. **Exact verification**: compute the true Jaccard similarity on the real
   shingle sets (token tuples, not hashes); keep pairs `>= threshold` and
   record evidence (score, shared/union counts, sample shared shingles).
5. **Cluster**: connected components (union-find) over verified edges only.

## Tokenization rules

- `text.casefold()` — Unicode-aware lowercasing.
- Extract maximal runs of `\w+` (`re.UNICODE`): letters, digits, underscore.
- Everything else (whitespace, punctuation, symbols) is a separator.
- Example: `"Hello, WORLD! 量子 计算"` → `["hello", "world", "量子", "计算"]`.

## Shingling rules (width k, default 3)

- `len(tokens) >= k`: all contiguous windows of width `k`.
- `0 < len(tokens) < k` (short text, defined): a single shingle equal to the
  whole token sequence — the document is its own only shingle.
- Empty text: the empty shingle set.

## Jaccard rules

- `J(A, B) = |A ∩ B| / |A ∪ B|`, computed exactly on shingle sets.
- Both sets empty → `1.0`; exactly one empty → `0.0`.

## Hash collisions

Shingles are hashed (blake2b-64) **only** to build the candidate inverted
index. A collision can introduce a spurious *candidate*, but the final
decision always comes from exact Jaccard on the real shingle sets, so
collisions can never change the result. `find_candidates` accepts an
injectable `hash_fn`; the test suite forces a constant hash (maximal
collision) and proves the verified edges are unaffected.

## Clustering rule (non-transitivity)

Similarity is **not** transitive: A~B and B~C does **not** imply A~C.
Clusters are connected components over verified edges, so cluster membership
claims *connectivity only*. Pairwise similarity is claimed **exclusively**
for the pairs listed in the report's `evidence` section. The demo fixture
contains exactly this trap: alpha~beta and beta~gamma, while alpha and
gamma share nothing — they land in one cluster with no alpha-gamma edge.

## Usage

```sh
python3.11 demo.py                                   # generate sample_docs/ + run
python3.11 simcluster.py sample_docs -k 3 -t 0.2     # text report
python3.11 simcluster.py sample_docs -k 3 -t 0.2 --json
python3.11 -m unittest discover -s tests -v          # tests
```

## Sample run (`python3.11 demo.py`)

```
== Verified similar pairs (evidence) ==
  alpha.txt ~ beta.txt: jaccard=0.3548 (11/31 shingles shared)
  beta.txt ~ gamma.txt: jaccard=0.2500 (8/32 shingles shared)
  delta.txt ~ epsilon.txt: jaccard=0.4444 (8/18 shingles shared)

== Clusters (connected components) ==
  cluster 0: alpha.txt, beta.txt, gamma.txt
      verified edges: [['alpha.txt', 'beta.txt'], ['beta.txt', 'gamma.txt']]
  cluster 1: delta.txt, epsilon.txt
  cluster 2: empty.txt
  cluster 3: short.txt
```

Note: alpha and gamma are in the same cluster but have **no** verified edge —
the report never claims they are pairwise similar.

## Test results

```
$ python3.11 -m unittest discover -s tests
Ran 18 tests in 0.004s
OK
```

Coverage: tokenization (casefold/unicode/empty), shingling (normal, k=1,
shorter-than-k fallback, empty, invalid k), exact Jaccard (incl. both-empty
definition), forced hash collision (candidates change, edges don't),
transitivity chain (one cluster, no phantom A-C edge), and end-to-end runs
on synthetic UTF-8 docs including short and empty files.
