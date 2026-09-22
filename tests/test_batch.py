import unittest

from pullpipe.batch import Batch, EOF


class TestBatch(unittest.TestCase):
    def test_empty_batch_is_truthy(self):
        empty = Batch()
        self.assertEqual(len(empty), 0)
        self.assertTrue(bool(empty), "空 Batch 必须为真：只有 None 才是 EOF")

    def test_eof_is_none(self):
        self.assertIsNone(EOF)

    def test_consumer_idiom_distinguishes_empty_from_eof(self):
        stream = [Batch(), Batch([(1,), (2,)]), Batch(), None]
        seen_rows = 0
        seen_empty = 0
        while True:
            batch = stream.pop(0)
            if batch is None:
            # EOF：退出
                break
            if len(batch) == 0:
                seen_empty += 1
                continue  # 空批次：不能退出
            seen_rows += len(batch)
        self.assertEqual(seen_empty, 2)
        self.assertEqual(seen_rows, 2)

    def test_batched_rows_bounded(self):
        b = Batch(range(10))
        self.assertEqual(b.rows, list(range(10)))


if __name__ == "__main__":
    unittest.main()
