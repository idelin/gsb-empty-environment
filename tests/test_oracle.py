import random
import unittest

from axcon import (Budget, Executor, SparseTensor, build_plan,
                   dense_contract)


def random_tensor(rng, name, labels, shape, nnz):
    if any(size == 0 for size in shape):
        return SparseTensor(name, labels, shape)
    capacity = 1  # distinct labels only: repeated labels share an index
    for label in dict.fromkeys(labels):
        capacity *= shape[labels.index(label)]
    nnz = min(nnz, capacity)
    entries = {}
    while len(entries) < nnz:
        coord = []
        seen = {}
        ok = True
        for label, size in zip(labels, shape):
            if label in seen:
                coord.append(seen[label])  # diagonal: reuse index
            else:
                if size == 0:
                    ok = False
                    break
                index = rng.randrange(size)
                seen[label] = index
                coord.append(index)
        if ok:
            entries[tuple(coord)] = float(rng.randint(1, 5))
    return SparseTensor(name, labels, shape, entries)


SPECS = [
    # (labels_per_input, shapes_per_input, output_labels)
    ([("i", "j"), ("j", "k")], [(3, 4), (4, 2)], ("i", "k")),
    ([("i",), ("i", "j")], [(3,), (3, 2)], ("j",)),
    ([("i",), ("j",)], [(3,), (2,)], ("i", "j")),         # outer product
    ([("i", "i"), ("i", "j")], [(3, 3), (3, 2)], ("i", "j")),  # diagonal
    ([(), ("i", "j")], [(), (2, 3)], ("i", "j")),          # scalar scale
    ([("i", "j"), ("i", "j")], [(2, 3), (2, 3)], ("j",)),  # reduce
    ([("i", "j"), ("j", "k"), ("k", "l")],
     [(3, 2), (2, 3), (3, 2)], ("i", "l")),                # 3-input chain
    ([("i", "j"), ("j",)], [(0, 3), (3,)], ("i",)),        # empty axis
]


class TestOracle(unittest.TestCase):
    def test_executor_matches_dense_oracle(self):
        rng = random.Random(20260925)
        for spec_i, (labels_list, shape_list, out) in enumerate(SPECS):
            for trial in range(4):
                tensors = [
                    random_tensor(rng, f"T{k}", labels_list[k],
                                  shape_list[k], nnz=rng.randint(0, 5))
                    for k in range(len(labels_list))
                ]
                plan = build_plan(tensors, out)
                budget = Budget(chunk_size=1 + trial,
                                max_entries_in_memory=2 + trial)
                result = Executor(budget).execute(plan, tensors)
                oracle = dense_contract(tensors, out)
                self.assertTrue(result.completed)
                self.assertEqual(result.tensor.entries, oracle,
                                 msg=f"spec {spec_i} trial {trial}")
                self.assertEqual(result.tensor.labels, out)


if __name__ == "__main__":
    unittest.main()
