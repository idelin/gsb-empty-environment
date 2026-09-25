import gc
import unittest

import mini_autograd as ma


class TestStorageRefcount(unittest.TestCase):
    def test_alias_keeps_storage_alive(self):
        t = ma.tensor([1.0, 2.0, 3.0], requires_grad=True)
        sid = t.storage.id
        alias = t.share_storage()
        self.assertEqual(t.storage.refcount, 2)
        self.assertTrue(t.storage.alive)

        with ma.Graph().use():
            y = ma.sum(ma.mul(t, t))
            y.backward()

        self.assertEqual(alias.data, [1.0, 2.0, 3.0])
        self.assertEqual(sid, alias.storage.id)
        del t
        gc.collect()
        self.assertTrue(alias.storage.alive)

    def test_release_recycles_when_unreferenced(self):
        t = ma.tensor([1.0, 2.0], requires_grad=True)
        storage = t.storage
        self.assertEqual(storage.refcount, 1)
        del t
        gc.collect()
        self.assertFalse(storage.alive)

    def test_graph_release_does_not_free_shared_storage(self):
        with ma.Graph("g1").use():
            shared_param = ma.tensor([2.0, -1.0], requires_grad=True)
            l1 = ma.sum(ma.relu(shared_param))
        with ma.Graph("g2").use():
            l2 = ma.sum(ma.mul(shared_param, shared_param))

        l1.backward()
        self.assertTrue(shared_param.storage.alive)
        self.assertEqual(shared_param.data, [2.0, -1.0])
        l2.backward()
        # l1 contributed relu grad [1,0]; l2 contributed 2p [4,-2]
        self.assertEqual(shared_param.grad.data, [5.0, -2.0])

    def test_use_after_free_is_detected(self):
        t = ma.tensor([1.0], requires_grad=True)
        storage = t.storage
        alias = t.share_storage()
        del t
        del alias
        gc.collect()
        self.assertFalse(storage.alive)


if __name__ == "__main__":
    unittest.main()
