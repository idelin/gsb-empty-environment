"""Constrained lottery assignment with reviewable, seed-deterministic results."""

from .prng import SplitMix64
from .solver import Problem, solve, brute_force_lexmin

__all__ = ["SplitMix64", "Problem", "solve", "brute_force_lexmin"]
