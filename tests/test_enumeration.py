"""Exhaustive small-domain enumeration against the independent reference.

Covers: empty dims, all-mask slices, +-inf, ties, negative/permuted axes,
keepdims, chunked tree execution, gradient values and nondiff marks.
"""
import math
import unittest
from itertools import product

from tinyreduce import Tensor, EmptyReductionError, normalize_axes, value_and_grad
from tests import reference

OPS = ("sum", "mean", "min", "max")
DOMAIN2 = (-math.inf, -1.0, 0.5, math.inf)          # (2,2): 4^4 = 256 combos
DOMAIN3 = (-math.inf, -2.0, 0.0, 1.5, math.inf)     # (3,):  5^3 = 125 combos


def nest(flat, shape):
    if len(shape) == 1:
        return list(flat)
    step = len(flat) // shape[0]
    return [nest(flat[i * step:(i + 1) * step], shape[1:])
            for i in range(shape[0])]


def mask_set(mask_flat, shape):
    return {idx for idx, m in zip(reference.indices(shape), mask_flat) if m}


def lib_out_dict(t, op, axes, keepdims, mask, chunk_size):
    out = getattr(t, op)(axis=axes, keepdims=keepdims, mask=mask,
                         chunk_size=chunk_size)
    reduced = set(normalize_axes(axes, t.ndim))
    result = {}
    for idx in out.indices():
        if keepdims:
            key = tuple(v for d, v in enumerate(idx) if d not in reduced)
        else:
            key = idx
        result[key] = out.get(idx)
    return result


class TestEnumeration22(unittest.TestCase):
    """All 4^4 value combos x all 2^4 masks x axis configs x 4 ops."""

    def test_forward_and_grad(self):
        shape = (2, 2)
        checked = raised = 0
        for vals in product(DOMAIN2, repeat=4):
            nested = nest(vals, shape)
            for mask_bits in product((False, True), repeat=4):
                masked = mask_set(mask_bits, shape)
                mask_t = Tensor(nest([1.0 if b else 0.0 for b in mask_bits],
                                     shape))
                if all(mask_bits):
                    axes_cfgs = [(None, False)]          # all-mask: one pass
                elif any(mask_bits):
                    axes_cfgs = [(a, False)
                                 for a in (None, (0,), (0, 1))]
                else:
                    axes_cfgs = [(a, kd)
                                 for a in (None, (0,), (1,), (0, 1), (1, 0),
                                           (-1, -2))
                                 for kd in (False, True)]
                for axes, keepdims in axes_cfgs:
                    axes_n = normalize_axes(axes, 2)
                    for op in OPS:
                        try:
                            ref_out, ref_grad, ref_nd = \
                                reference.forward_and_grad(
                                    nested, shape, axes_n, op, masked)
                        except reference.RefEmptyError:
                            with self.assertRaises(EmptyReductionError):
                                getattr(Tensor(nested), op)(
                                    axis=axes, mask=mask_t)
                            raised += 1
                            continue
                        t = Tensor(nested)
                        got = lib_out_dict(t, op, axes, keepdims, mask_t,
                                           chunk_size=None)
                        self.assertEqual(got.keys(), ref_out.keys())
                        for k in ref_out:
                            self.assertTrue(
                                reference.close(got[k], ref_out[k]),
                                f"{op}{axes} {nested} mask={mask_bits} @{k}: "
                                f"{got[k]} != {ref_out[k]}")
                        # gradient + nondiff marks (keepdims-independent)
                        _, g = value_and_grad(Tensor(nested), op, axis=axes,
                                              mask=mask_t)
                        for idx in reference.indices(shape):
                            self.assertTrue(
                                reference.close(g.get(idx), ref_grad[idx]),
                                f"d{op}{axes} {nested} mask={mask_bits} "
                                f"@{idx}: {g.get(idx)} != {ref_grad[idx]}")
                        self.assertEqual(set(g.nondiff_indices), ref_nd)
                        checked += 1
        # sanity: the enumeration really exercised both branches heavily
        self.assertGreater(checked, 40_000)
        self.assertGreater(raised, 5_000)


class TestEnumeration3(unittest.TestCase):
    """5^3 combos x 2^3 masks; every case run with two chunkings."""

    def test_chunked_matches_reference(self):
        shape = (3,)
        checked = 0
        for vals in product(DOMAIN3, repeat=3):
            nested = list(vals)
            for mask_bits in product((False, True), repeat=3):
                masked = mask_set(mask_bits, shape)
                mask_t = Tensor([1.0 if b else 0.0 for b in mask_bits])
                for axes in (None, (0,)):
                    axes_n = normalize_axes(axes, 1)
                    for op in OPS:
                        try:
                            ref_out, ref_grad, ref_nd = \
                                reference.forward_and_grad(
                                    nested, shape, axes_n, op, masked)
                        except reference.RefEmptyError:
                            with self.assertRaises(EmptyReductionError):
                                getattr(Tensor(nested), op)(mask=mask_t)
                            continue
                        for cs in (None, 1, 2):
                            got = lib_out_dict(Tensor(nested), op, axes,
                                               False, mask_t, cs)
                            for k in ref_out:
                                self.assertTrue(
                                    reference.close(got[k], ref_out[k]),
                                    f"{op} cs={cs} {nested} @{k}")
                            _, g = value_and_grad(Tensor(nested), op,
                                                  axis=axes, mask=mask_t,
                                                  chunk_size=cs)
                            for idx in reference.indices(shape):
                                self.assertTrue(
                                    reference.close(g.get(idx),
                                                    ref_grad[idx]))
                            self.assertEqual(set(g.nondiff_indices), ref_nd)
                        checked += 1
        self.assertGreater(checked, 5_000)


class TestEmptyDimEnumeration(unittest.TestCase):
    def test_zero_size_shapes(self):
        cases = [([], (0,)), ([[], []], (2, 0)), ([[], []], (2, 0)),
                 ([[], [], []], (3, 0))]
        for nested, shape in cases:
            t = Tensor(nested)
            for axes in [None] + [(a,) for a in range(len(shape))]:
                axes_n = normalize_axes(axes, len(shape))
                for op in OPS:
                    try:
                        ref_out, _, _ = reference.forward_and_grad(
                            nested, shape, axes_n, op)
                    except reference.RefEmptyError:
                        with self.assertRaises(EmptyReductionError):
                            getattr(t, op)(axis=axes)
                        continue
                    got = lib_out_dict(t, op, axes, False, None, None)
                    self.assertEqual(got.keys(), ref_out.keys())
                    for k in ref_out:
                        self.assertTrue(reference.close(got[k], ref_out[k]))

    def test_mixed_empty_nonempty(self):
        # (2,0,3): reducing axis 1 -> empty slices; axes (0,2) -> 0 slices
        t = Tensor.zeros((2, 0, 3))
        self.assertEqual(t.sum(axis=1).to_flat(), [0.0] * 6)
        for op in ("mean", "min", "max"):
            with self.assertRaises(EmptyReductionError):
                getattr(t, op)(axis=1)
            out = getattr(t, op)(axis=(0, 2))
            self.assertEqual(out.shape, (0,))


if __name__ == "__main__":
    unittest.main()
