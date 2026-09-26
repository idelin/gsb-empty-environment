import unittest

from automata.symbols import Interval, SymbolSet, find_atom, partition, refine_atoms


class TestInterval(unittest.TestCase):
    def test_invalid_interval_rejected(self):
        with self.assertRaises(ValueError):
            Interval(5, 4)

    def test_contains_and_size(self):
        iv = Interval(3, 7)
        self.assertEqual(iv.size, 5)
        self.assertTrue(iv.contains(3))
        self.assertTrue(iv.contains(7))
        self.assertFalse(iv.contains(8))


class TestSymbolSet(unittest.TestCase):
    def test_normalize_merges_overlapping_and_adjacent(self):
        ss = SymbolSet([Interval(6, 10), Interval(0, 5), Interval(4, 7), Interval(20, 22)])
        self.assertEqual(ss.intervals, (Interval(0, 10), Interval(20, 22)))

    def test_union(self):
        a = SymbolSet.range(0, 5)
        b = SymbolSet.range(3, 9)
        self.assertEqual(a.union(b).intervals, (Interval(0, 9),))

    def test_intersect(self):
        a = SymbolSet([Interval(0, 10), Interval(20, 30)])
        b = SymbolSet([Interval(5, 25)])
        self.assertEqual(a.intersect(b).intervals, (Interval(5, 10), Interval(20, 25)))

    def test_difference(self):
        a = SymbolSet.range(0, 10)
        b = SymbolSet([Interval(3, 4), Interval(7, 20)])
        self.assertEqual(a.difference(b).intervals,
                         (Interval(0, 2), Interval(5, 6)))

    def test_complement_large_universe_without_enumeration(self):
        uni = SymbolSet.range(0, 2**32 - 1)
        mid = SymbolSet.range(2**20, 2**21)
        comp = mid.complement(uni)
        self.assertEqual(comp.intervals,
                         (Interval(0, 2**20 - 1), Interval(2**21 + 1, 2**32 - 1)))
        self.assertEqual(comp.size + mid.size, uni.size)

    def test_char_classes(self):
        digits = SymbolSet.char_range("0", "9")
        self.assertTrue(digits.contains(ord("5")))
        self.assertFalse(digits.contains(ord("a")))
        self.assertEqual(SymbolSet.chars("ab").intervals, (Interval(97, 98),))
        self.assertEqual(SymbolSet.chars("ac").intervals,
                         (Interval(97, 97), Interval(99, 99)))

    def test_equality_and_hash(self):
        a = SymbolSet([Interval(0, 3), Interval(2, 5)])
        b = SymbolSet.range(0, 5)
        self.assertEqual(a, b)
        self.assertEqual(hash(a), hash(b))


class TestPartition(unittest.TestCase):
    def test_overlapping_char_classes(self):
        uni = SymbolSet.range(0, 127)
        atoms = partition(
            [SymbolSet.char_range("a", "z"), SymbolSet.char_range("A", "Z"),
             SymbolSet.char_range("0", "9")],
            uni,
        )
        self.assertEqual(
            atoms,
            (Interval(0, 47), Interval(48, 57), Interval(58, 64),
             Interval(65, 90), Interval(91, 96), Interval(97, 122),
             Interval(123, 127)),
        )

    def test_partition_covers_universe_exactly(self):
        uni = SymbolSet([Interval(0, 9), Interval(100, 109)])
        atoms = partition([SymbolSet.range(3, 103)], uni)
        self.assertEqual(atoms, (Interval(0, 2), Interval(3, 9),
                                 Interval(100, 103), Interval(104, 109)))

    def test_refine_atoms_merges_boundaries(self):
        a = partition([SymbolSet.range(0, 100)], SymbolSet.range(0, 200))
        b = partition([SymbolSet.range(50, 150)], SymbolSet.range(0, 200))
        self.assertEqual(refine_atoms(a, b),
                         (Interval(0, 49), Interval(50, 100),
                          Interval(101, 150), Interval(151, 200)))

    def test_find_atom_outside_alphabet(self):
        atoms = partition([], SymbolSet.range(0, 200))
        self.assertEqual(find_atom(atoms, Interval(0, 200)), Interval(0, 200))
        self.assertIsNone(find_atom(atoms, Interval(201, 300)))


if __name__ == "__main__":
    unittest.main()
