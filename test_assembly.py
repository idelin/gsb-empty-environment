"""Unit tests for the incremental assembler (stdlib unittest only)."""

import math
import unittest

from assembly import Assembler, AssemblyError, assemble_reference, contribution
from dense import matvec, solve_dense

KE = [[1.0, -1.0], [-1.0, 1.0]]
FE = [0.5, 0.5]


def bar_elements(stiffness=None):
    stiffness = stiffness or {}
    return [
        contribution(f"e{k}", [k, k + 1],
                     [[s, -s], [-s, s]] if (s := stiffness.get(f"e{k}")) else KE,
                     FE)
        for k in range(3)
    ]


def assert_system_close(test, asm, ref_a, ref_b):
    for i in range(asm.size):
        for j in range(asm.size):
            test.assertTrue(
                math.isclose(asm.matrix[i][j], ref_a[i][j], rel_tol=1e-12, abs_tol=1e-12),
                f"A[{i}][{j}]: {asm.matrix[i][j]} != {ref_a[i][j]}",
            )
        test.assertTrue(
            math.isclose(asm.rhs[i], ref_b[i], rel_tol=1e-12, abs_tol=1e-12),
            f"b[{i}]: {asm.rhs[i]} != {ref_b[i]}",
        )


class SharedDofTests(unittest.TestCase):
    def test_shared_entry_contributions_add(self):
        asm = Assembler(4)
        asm.add_elements(bar_elements())
        # Node 1 and 2 are shared by two elements each: 1 + 1 = 2.
        self.assertEqual(asm.matrix[1][1], 2.0)
        self.assertEqual(asm.matrix[2][2], 2.0)
        self.assertEqual(asm.rhs[1], 1.0)
        prov = asm.entry_provenance(1, 1)
        self.assertEqual(prov["elements"], {"e0": 1.0, "e1": 1.0})

    def test_sign_cancellation(self):
        asm = Assembler(2)
        asm.add_elements([
            contribution("plus", [0, 1], [[5.0, 1.0], [1.0, 5.0]]),
            contribution("minus", [0, 1], [[-5.0, -1.0], [-1.0, -5.0]]),
        ])
        self.assertEqual(asm.matrix[0][0], 0.0)
        self.assertEqual(asm.matrix[0][1], 0.0)
        prov = asm.entry_provenance(0, 0)
        self.assertEqual(prov["elements"], {"plus": 5.0, "minus": -5.0})
        self.assertEqual(prov["value"], 0.0)

    def test_repeated_dof_inside_one_map(self):
        asm = Assembler(2)
        asm.add_elements([contribution("loop", [0, 0], [[1.0, 1.0], [1.0, 1.0]])])
        self.assertEqual(asm.matrix[0][0], 4.0)  # (0,0) hit twice by map [0, 0]


class ConstraintTests(unittest.TestCase):
    def test_elimination_corrects_rhs(self):
        asm = Assembler(4)
        asm.add_elements(bar_elements())
        asm.set_constraints({0: 0.0, 3: 0.5})
        # b_2 must receive the correction -A_23 * u3 = -(-1) * 0.5 = +0.5.
        self.assertEqual(asm.rhs[2], 1.5)
        self.assertEqual(asm.rhs[3], 0.5)
        self.assertEqual(asm.matrix[3], [0.0, 0.0, 0.0, 1.0])
        self.assertEqual([asm.matrix[i][3] for i in range(4)], [0.0, 0.0, 0.0, 1.0])
        prov = asm.rhs_provenance(2)
        self.assertEqual(prov["constraint_corrections"], {3: -0.5})
        u = solve_dense(asm.matrix, asm.rhs)
        self.assertTrue(math.isclose(u[1], 7.0 / 6.0, rel_tol=1e-12))
        self.assertTrue(math.isclose(u[2], 4.0 / 3.0, rel_tol=1e-12))
        residual = matvec(asm.matrix, u)
        for got, want in zip(residual, asm.rhs):
            self.assertTrue(math.isclose(got, want, rel_tol=1e-12, abs_tol=1e-12))

    def test_constraint_change_reassembles_incrementally(self):
        elements = bar_elements()
        asm = Assembler(4)
        asm.add_elements(elements)
        asm.set_constraints({0: 0.0, 3: 0.5})
        v1 = asm.constraint_version
        asm.set_constraints({0: 0.0, 3: 1.25})
        self.assertEqual(asm.constraint_version, v1 + 1)
        ref_a, ref_b = assemble_reference(4, elements, {0: 0.0, 3: 1.25})
        assert_system_close(self, asm, ref_a, ref_b)
        # Dropping all constraints must restore the unconstrained system.
        asm.set_constraints({})
        ref_a, ref_b = assemble_reference(4, elements, {})
        assert_system_close(self, asm, ref_a, ref_b)

    def test_constraint_version_in_provenance(self):
        asm = Assembler(3)
        asm.add_elements([contribution("e0", [0, 1], KE, FE),
                          contribution("e1", [1, 2], KE, FE)])
        asm.set_constraints({2: 3.0})
        prov = asm.entry_provenance(1, 2)
        self.assertEqual(prov["constraint_version"], asm.constraint_version)
        self.assertEqual(prov["constraints"], [{"dof": 2, "value": 3.0}])
        asm.set_constraints({2: 4.0})
        self.assertEqual(asm.entry_provenance(1, 2)["constraint_version"],
                         asm.constraint_version)

    def test_invalid_constraint_rejected(self):
        asm = Assembler(2)
        with self.assertRaises(AssemblyError):
            asm.set_constraints({5: 1.0})
        self.assertEqual(asm.constraints, {})


class MutationTests(unittest.TestCase):
    def test_remove_element_matches_full_assembly(self):
        elements = bar_elements()
        asm = Assembler(4)
        asm.add_elements(elements)
        asm.set_constraints({0: 0.0, 3: 0.5})
        asm.remove_element("e1")
        remaining = [elements[0], elements[2]]
        ref_a, ref_b = assemble_reference(4, remaining, {0: 0.0, 3: 0.5})
        assert_system_close(self, asm, ref_a, ref_b)
        self.assertEqual(asm.entry_provenance(1, 1)["elements"], {"e0": 1.0})

    def test_update_element_matches_full_assembly(self):
        elements = bar_elements()
        asm = Assembler(4)
        asm.add_elements(elements)
        asm.set_constraints({0: 0.0, 3: 0.5})
        asm.update_element("e1", [1, 2], [[2.0, -2.0], [-2.0, 2.0]], [1.0, 1.0])
        updated = bar_elements(stiffness={"e1": 2.0})
        updated[1] = contribution("e1", [1, 2], [[2.0, -2.0], [-2.0, 2.0]], [1.0, 1.0])
        ref_a, ref_b = assemble_reference(4, updated, {0: 0.0, 3: 0.5})
        assert_system_close(self, asm, ref_a, ref_b)

    def test_update_does_not_disturb_other_elements(self):
        asm = Assembler(4)
        asm.add_elements(bar_elements())
        before = asm.entry_provenance(3, 3)["elements"]
        asm.update_element("e0", [0, 1], [[3.0, -3.0], [-3.0, 3.0]], FE)
        self.assertEqual(asm.entry_provenance(3, 3)["elements"], before)
        self.assertEqual(asm.matrix[3][3], 1.0)

    def test_update_unknown_element_rejected(self):
        asm = Assembler(2)
        with self.assertRaises(AssemblyError):
            asm.update_element("nope", [0, 1], KE)


class BatchTests(unittest.TestCase):
    def test_invalid_mapping_rejects_whole_batch(self):
        asm = Assembler(4)
        asm.add_elements([contribution("ok", [0, 1], KE, FE)])
        snapshot = [row[:] for row in asm.matrix]
        with self.assertRaises(AssemblyError):
            asm.add_elements([
                contribution("good", [1, 2], KE, FE),
                contribution("bad", [2, 99], KE, FE),   # dof 99 out of range
            ])
        self.assertEqual(asm.element_ids(), ["ok"])
        self.assertEqual(asm.matrix, snapshot)

    def test_shape_mismatch_rejected(self):
        asm = Assembler(3)
        with self.assertRaises(AssemblyError):
            asm.add_elements([contribution("bad", [0, 1], [[1.0]])])
        with self.assertRaises(AssemblyError):
            asm.add_elements([contribution("bad", [0, 1], KE, [1.0])])
        self.assertEqual(asm.element_ids(), [])

    def test_duplicate_ids_rejected(self):
        asm = Assembler(2)
        with self.assertRaises(AssemblyError):
            asm.add_elements([contribution("x", [0, 1], KE),
                              contribution("x", [0, 1], KE)])
        asm.add_elements([contribution("x", [0, 1], KE)])
        with self.assertRaises(AssemblyError):
            asm.add_elements([contribution("x", [0, 1], KE)])

    def test_incremental_equals_full_end_to_end(self):
        elements = bar_elements(stiffness={"e1": 2.5})
        asm = Assembler(4)
        asm.add_elements(elements[:2])
        asm.add_elements(elements[2:])
        asm.set_constraints({0: 0.0})
        asm.update_element("e0", [0, 1], [[4.0, -4.0], [-4.0, 4.0]], [2.0, 2.0])
        asm.set_constraints({0: 0.0, 3: -0.75})
        final = [
            contribution("e0", [0, 1], [[4.0, -4.0], [-4.0, 4.0]], [2.0, 2.0]),
            elements[1],
            elements[2],
        ]
        ref_a, ref_b = assemble_reference(4, final, {0: 0.0, 3: -0.75})
        assert_system_close(self, asm, ref_a, ref_b)


if __name__ == "__main__":
    unittest.main()
