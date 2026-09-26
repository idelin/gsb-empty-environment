import random
import unittest

from topk import StaleCursorError, TopKIndex, tokenize


def ref_display(aliases):
    return min(aliases.items(), key=lambda kv: (-kv[1], kv[0].casefold(), kv[0]))[0]


def ref_suggest(model, aliases, prefix, limit, after=None):
    """Full-sort reference: scans every word, sorts, paginates."""
    norm_prefix = prefix.casefold()
    keys = sorted((-f, w) for w, f in model.items() if w.startswith(norm_prefix))
    if after is not None:
        after_key = (-after[0], after[1])
        keys = [k for k in keys if k > after_key]
    out = []
    for negf, w in keys[:limit]:
        out.append((w, ref_display(aliases[w]), -negf))
    return out


def as_tuples(suggestions):
    return [(s.word, s.display, s.freq) for s in suggestions]


class TokenizerTest(unittest.TestCase):
    def test_rules(self):
        toks = list(tokenize("Hello, WORLD! foo_bar x42 3.14 naïve café"))
        self.assertEqual(
            [(t.display, t.normalized) for t in toks],
            [
                ("Hello", "hello"),
                ("WORLD", "world"),
                ("foo_bar", "foo_bar"),
                ("x42", "x42"),
                ("3", "3"),
                ("14", "14"),
                ("naïve", "naïve"),
                ("café", "café"),
            ],
        )

    def test_casefold_identity(self):
        toks = list(tokenize("Straße STRASSE strasse"))
        self.assertEqual([t.normalized for t in toks], ["strasse", "strasse", "strasse"])

    def test_utf8_cjk(self):
        toks = list(tokenize("北京，上海。apple苹果"))
        self.assertEqual([t.display for t in toks], ["北京", "上海", "apple苹果"])


class BasicRankingTest(unittest.TestCase):
    def test_rank_and_stable_tiebreak(self):
        idx = TopKIndex()
        for w, f in [("banana", 5), ("apple", 5), ("cherry", 5), ("date", 9)]:
            idx.set_freq(w, f)
        got = [s.word for s in idx.suggest("", 4)]
        self.assertEqual(got, ["date", "apple", "banana", "cherry"])

    def test_alias_aggregation_and_display_choice(self):
        idx = TopKIndex()
        idx.add("Apple", 2)
        idx.add("APPLE", 5)
        idx.add("apple", 3)
        self.assertEqual(idx.freq("aPpLe"), 10)
        s = idx.suggest("app", 1)[0]
        self.assertEqual(s.display, "APPLE")  # most frequent alias
        self.assertEqual(s.freq, 10)

    def test_display_tiebreak_deterministic(self):
        idx = TopKIndex()
        idx.add("Beta", 1)
        idx.add("BETA", 1)
        idx.add("beta", 1)
        self.assertEqual(idx.suggest("bet", 1)[0].display, "BETA")

    def test_word_that_is_prefix_of_another(self):
        idx = TopKIndex()
        idx.set_freq("app", 7)
        idx.set_freq("apple", 3)
        idx.set_freq("application", 1)
        got = [s.word for s in idx.suggest("app", 10)]
        self.assertEqual(got, ["app", "apple", "application"])

    def test_empty_prefix_respects_limit(self):
        idx = TopKIndex()
        for i in range(50):
            idx.set_freq("w%02d" % i, i + 1)
        self.assertEqual(len(idx.suggest("", 5)), 5)
        self.assertEqual(len(idx.suggest("", 100)), 50)

    def test_prefix_case_insensitive(self):
        idx = TopKIndex()
        idx.add("Apple", 4)
        self.assertEqual([s.word for s in idx.suggest("APP", 5)], ["apple"])

    def test_delete_backfills_next_candidate(self):
        idx = TopKIndex(topk_capacity=3)
        for w, f in [("alpha", 100), ("alps", 90), ("alto", 80), ("alum", 70)]:
            idx.set_freq(w, f)
        self.assertEqual([s.word for s in idx.suggest("al", 3)], ["alpha", "alps", "alto"])
        idx.remove("alpha")
        got = idx.suggest("al", 3)
        self.assertEqual([s.word for s in got], ["alps", "alto", "alum"])

    def test_freq_decrease_backfills(self):
        idx = TopKIndex(topk_capacity=2)
        idx.set_freq("top", 100)
        idx.set_freq("toy", 50)
        idx.set_freq("toe", 10)
        idx.set_freq("top", 5)  # drop below the others
        self.assertEqual([s.word for s in idx.suggest("to", 2)], ["toy", "toe"])

    def test_remove_missing_returns_false(self):
        idx = TopKIndex()
        self.assertFalse(idx.remove("nope"))
        idx.set_freq(0 and "x" or "x", 3)
        self.assertTrue(idx.remove("X"))
        self.assertNotIn("x", idx)

    def test_set_freq_zero_removes(self):
        idx = TopKIndex()
        idx.set_freq("gone", 4)
        idx.set_freq("gone", 0)
        self.assertNotIn("gone", idx)
        self.assertEqual(idx.suggest("gon", 5), [])


class PaginationTest(unittest.TestCase):
    def test_rank_cursor_pagination_matches_full_list(self):
        idx = TopKIndex(topk_capacity=4)
        words = {}
        for i in range(37):
            words["p%02d" % i] = (i * 7) % 13 + 1
        for w, f in words.items():
            idx.set_freq(w, f)
        full = idx.suggest("p", 100)
        pages, after = [], None
        while True:
            page = idx.suggest("p", 4, after=after)
            if not page:
                break
            pages.extend(page)
            after = (page[-1].freq, page[-1].word)
            self.assertLessEqual(len(page), 4)
        self.assertEqual(as_tuples(pages), as_tuples(full))
        self.assertGreater(len(pages), idx._capacity)  # deep pages really happened

    def test_pagination_beyond_cache_uses_best_first(self):
        idx = TopKIndex(topk_capacity=3)
        for i in range(20):
            idx.set_freq("k%02d" % i, 100 - i)
        got = idx.suggest("k", 20)
        self.assertEqual([s.word for s in got], ["k%02d" % i for i in range(20)])
        after = (got[4].freq, got[4].word)
        rest = idx.suggest("k", 7, after=after)
        self.assertEqual([s.word for s in rest], ["k%02d" % i for i in range(5, 12)])


class VersionedCacheTest(unittest.TestCase):
    def test_versions_and_lazy_recompute(self):
        idx = TopKIndex()
        idx.set_freq("aa", 1)
        v1 = idx._root.version
        idx.set_freq("ab", 2)
        self.assertGreater(idx._root.version, v1)
        self.assertTrue(idx._root.dirty)
        idx.suggest("", 5)
        self.assertFalse(idx._root.dirty)

    def test_no_full_subtree_scan_on_query(self):
        # A clean (fresh) cache must answer without touching children.
        idx = TopKIndex(topk_capacity=4)
        for i in range(30):
            idx.set_freq("s%02d" % i, i)
        idx.suggest("s", 4)  # warm caches
        touched = []

        orig = TopKIndex._ensure_fresh

        def spy(self, slot):
            if slot.dirty:
                touched.append(slot.label)
            return orig(self, slot)

        TopKIndex._ensure_fresh = spy
        try:
            idx.suggest("s", 4)
        finally:
            TopKIndex._ensure_fresh = orig
        self.assertEqual(len(touched), 0)


class CompactionTest(unittest.TestCase):
    def _build(self):
        idx = TopKIndex(topk_capacity=4)
        for w, f in [("apple", 50),("app", 40),("apricot", 30),("banana", 20),
                     ("band", 10),("can", 5),("candy", 4),("dog", 3)]:
            idx.set_freq(w, f)
        for w in ["band", "can"]:
            idx.remove(w)  # create garbage slots via compression
        return idx

    def test_compact_reclaims_and_preserves_results(self):
        idx = self._build()
        before_stats = idx.stats()
        self.assertGreater(before_stats["garbage_slots"], 0)
        before = as_tuples(idx.suggest("", 8))
        gen = idx.generation
        idx.compact()
        self.assertEqual(idx.generation, gen + 1)
        after_stats = idx.stats()
        self.assertEqual(after_stats["garbage_slots"], 0)
        self.assertLess(after_stats["arena_slots"], before_stats["arena_slots"])
        self.assertEqual(as_tuples(idx.suggest("", 8)), before)

    def test_old_cursor_reads_old_version_after_compaction(self):
        idx = self._build()
        snapshot = as_tuples(idx.suggest("a", 4))
        cursor = idx.open_cursor("a")
        idx.compact()
        self.assertTrue(cursor.stale)
        idx.set_freq("apple", 999)  # mutate after compaction
        idx.remove("app")
        got = as_tuples(cursor.read(4))
        self.assertEqual(got, snapshot)  # still the old version

    def test_strict_cursor_fails_after_compaction(self):
        idx = self._build()
        cursor = idx.open_cursor("a", strict=True)
        idx.compact()
        with self.assertRaises(StaleCursorError):
            cursor.read(4)

    def test_strict_cursor_fails_after_mutation(self):
        idx = self._build()
        cursor = idx.open_cursor("a", strict=True)
        idx.set_freq("apple", 51)
        with self.assertRaises(StaleCursorError):
            cursor.read(4)

    def test_nonstrict_cursor_sees_mutation_without_compaction(self):
        idx = self._build()
        cursor = idx.open_cursor("a")
        idx.set_freq("apricot", 500)
        got = cursor.read(1)
        self.assertEqual(got[0].word, "apricot")

    def test_cursor_on_missing_prefix(self):
        idx = self._build()
        self.assertEqual(idx.open_cursor("zzz").read(5), [])


class RandomizedReferenceTest(unittest.TestCase):
    def test_random_ops_against_full_sort(self):
        rng = random.Random(20260926)
        idx = TopKIndex(topk_capacity=8)
        model: dict[str, int] = {}
        aliases: dict[str, dict[str, int]] = {}

        pool = []
        base = ["app", "apple", "ape", "ban", "band", "can", "cat", "do", "dog"]
        for b in base:
            for suffix in ["", "e", "er", "ing", "s", "x"]:
                pool.append(b + suffix)
        pool += ["café", "Café", "CAFÉ", "Straße", "strasse", "北京", "naïve", "Naïve"]

        def variants(w):
            vs = {w, w.upper(), w.capitalize()}
            return sorted(vs)

        def apply_add(word, delta):
            norm = word.casefold()
            model[norm] = model.get(norm, 0) + delta
            a = aliases.setdefault(norm, {})
            a[word] = a.get(word, 0) + delta
            idx.add(word, delta)

        def check(prefix, limit, after=None):
            got = as_tuples(idx.suggest(prefix, limit, after=after))
            want = ref_suggest(model, aliases, prefix, limit, after=after)
            self.assertEqual(got, want, msg="prefix=%r limit=%r after=%r" % (prefix, limit, after))

        for step in range(4000):
            op = rng.random()
            word = rng.choice(pool)
            if op < 0.45:
                apply_add(word, rng.randint(1, 3))
            elif op < 0.65:
                f = rng.randint(0, 12)
                norm = word.casefold()
                if f == 0:
                    model.pop(norm, None)
                    aliases.pop(norm, None)
                    idx.remove(word)
                else:
                    model[norm] = f
                    a = aliases.setdefault(norm, {})
                    a[word] = a.get(word, 0) + 1
                    idx.set_freq(word, f)
            elif op < 0.80:
                norm = word.casefold()
                removed = idx.remove(word)
                self.assertEqual(removed, norm in model)
                model.pop(norm, None)
                aliases.pop(norm, None)
            else:
                base_word = rng.choice(pool).casefold()
                cut = rng.randint(0, len(base_word))
                prefix = base_word[:cut]
                limit = rng.randint(1, 15)
                check(prefix, limit)
                if rng.random() < 0.3:
                    ranked = sorted((-f, w) for w, f in model.items() if w.startswith(prefix))
                    if ranked:
                        negf, w = ranked[rng.randrange(len(ranked))]
                        check(prefix, limit, after=(-negf, w))

        # Full pagination sweep over several prefixes, page size 3.
        for prefix in ["", "a", "ap", "ca", "b", "北京".casefold()[:1] or ""]:
            after, seen = None, []
            for _ in range(200):
                page = idx.suggest(prefix, 3, after=after)
                if not page:
                    break
                seen.extend(as_tuples(page))
                after = (page[-1].freq, page[-1].word)
            self.assertEqual(seen, ref_suggest(model, aliases, prefix, 10 ** 9))

        # Compaction must not change any observable result.
        idx.compact()
        for prefix in ["", "a", "ap", "ca", "c"]:
            check(prefix, 12)


class LoadTextTest(unittest.TestCase):
    def test_load_text_aggregates_aliases(self):
        idx = TopKIndex()
        n = idx.load_text("Apple pie, apple tart. APPLE! apple? Apple.")
        self.assertEqual(n, 7)
        self.assertEqual(idx.freq("apple"), 5)
        s = idx.suggest("app", 1)[0]
        self.assertEqual(s.display, "Apple")
        self.assertEqual(s.freq, 5)


if __name__ == "__main__":
    unittest.main()
