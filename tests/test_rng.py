import unittest

from bootci.rng import StreamFactory, derive_seed


class TestStreamFactory(unittest.TestCase):
    def test_deterministic_same_stream_id(self):
        f1, f2 = StreamFactory(42), StreamFactory(42)
        s1 = [f1.stream("a").random() for _ in range(5)]
        # 重新创建工厂与流，序列必须一致
        s2 = [f2.stream("a").random() for _ in range(5)]
        self.assertEqual(s1, s2)

    def test_distinct_streams_differ(self):
        f = StreamFactory(42)
        a = [f.stream("a").random() for _ in range(5)]
        b = [f.stream("b").random() for _ in range(5)]
        self.assertNotEqual(a, b)

    def test_order_independence(self):
        # 并行/顺序无关：先建 b 再建 a，与先建 a 再建 b 完全一致
        f = StreamFactory(7)
        a1, b1 = f.stream("a").random(), f.stream("b").random()
        f = StreamFactory(7)
        b2, a2 = f.stream("b").random(), f.stream("a").random()
        self.assertEqual(a1, a2)
        self.assertEqual(b1, b2)

    def test_seed_type_checked(self):
        with self.assertRaises(TypeError):
            StreamFactory("42")
        with self.assertRaises(TypeError):
            StreamFactory(True)
        with self.assertRaises(TypeError):
            derive_seed(1.5, "x")


if __name__ == "__main__":
    unittest.main()
