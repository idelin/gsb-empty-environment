"""随机差分核验（不引入第三方库）：
- 贪心 UB 对所有未知字段子集/端点的枚举最优值不低估；
- 阈值融合与全量参考在随机语料、随机权重、随机页大小下逐位一致。
"""

import itertools
import random
import unittest

from fusion import (
    Document,
    Index,
    exhaustive_fuse,
    fuse_topk,
    greedy_upper_bound,
)

WORDS = ["alpha", "beta", "gamma", "delta", "epsilon", "zeta", "eta"]
CJK = ["检", "索", "融", "合", "归", "一", "化"]


def random_corpus(rng: random.Random) -> list[Document]:
    docs = []
    for i in range(rng.randint(1, 25)):
        fields = {}
        for name in ("title", "body", "tags"):
            if rng.random() < 0.75:
                vocab = CJK if rng.random() < 0.5 else WORDS
                fields[name] = " ".join(
                    rng.choice(vocab) for _ in range(rng.randint(0, 8)))
        if fields:
            docs.append(Document(doc_id=f"d{i:03d}", fields=fields))
    return docs


def brute_force_ub(known, caps, weights):
    unknown = [f for f in caps if f not in known and weights.get(f, 0) > 0]
    num = sum(weights[f] * n for f, n in known.items() if weights[f] > 0)
    den = sum(weights[f] for f in known if weights[f] > 0)
    best = num / den if den else 0.0
    for r in range(1, len(unknown) + 1):
        for subset in itertools.combinations(unknown, r):
            n2 = num + sum(weights[f] * caps[f] for f in subset)
            d2 = den + sum(weights[f] for f in subset)
            best = max(best, n2 / d2)
    return best


class TestRandomEquivalence(unittest.TestCase):
    def test_random(self):
        rng = random.Random(20260926)
        for trial in range(120):
            docs = random_corpus(rng)
            index = Index(docs)
            query = [rng.choice(WORDS + CJK) for _ in range(rng.randint(1, 3))]
            weights = {f: round(rng.choice([0, 1, 2, 3]) * rng.random(), 3)
                       for f in ("title", "body", "tags")}
            if all(w == 0 for w in weights.values()):
                weights["title"] = 1.0
            k = rng.randint(1, 6)
            ps = rng.choice([1, 2, 3, 5, 100])

            got = fuse_topk(index, query, weights, k=k, page_size=ps)
            ref = exhaustive_fuse(index, query, weights, k=k)
            g = [(h.doc_id, round(h.score, 10)) for h in got.hits]
            r = [(h.doc_id, round(h.score, 10)) for h in ref.hits]
            self.assertEqual(g, r, msg=f"trial {trial}: {query} {weights}")

            # 已知部分命中场景下的 UB 枚举核验
            caps = {}
            for f in ("title", "body", "tags"):
                if weights[f] > 0:
                    snap = index.get_snapshot(query, f)
                    caps[f] = snap.postings[0].norm if snap.postings else 0.0
            known = {}
            for f in caps:
                snap = index.get_snapshot(query, f)
                if snap.postings and rng.random() < 0.5:
                    known[f] = snap.postings[rng.randrange(
                        len(snap.postings))].norm
            if known:
                ub = greedy_upper_bound(known, caps, weights)
                bf = brute_force_ub(known, caps, weights)
                self.assertAlmostEqual(ub, bf, places=9,
                                       msg=f"UB trial {trial}")


if __name__ == "__main__":
    unittest.main()
