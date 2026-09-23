"""Exact bounded-derivation sampling for weighted context-free grammars.

Target distribution: over complete derivation trees whose yield length is
at most ``L`` and whose number of production applications is at most ``D``
(every application, including epsilon productions, consumes one step), each
tree is weighted by the product of its production weights and the finite
set of feasible trees is normalised by the total weight.  All arithmetic is
exact (big integers + ``fractions.Fraction``); randomness enters only
through a caller-supplied uniform integer source.
"""

from .grammar import Grammar, Production
from .tree import TreeNode, tree_weight, tree_yield
from .dp import BudgetExhaustedError, PartitionTable, ResourceBudget
from .sampler import BoundedSampler, Sample, randbelow_from_random
from .parser import string_mass
from .enumerator import EnumTree, enumerate_trees
from .validator import Validation, validate_tree

__all__ = [
    "Grammar",
    "Production",
    "TreeNode",
    "tree_weight",
    "tree_yield",
    "BudgetExhaustedError",
    "PartitionTable",
    "ResourceBudget",
    "BoundedSampler",
    "Sample",
    "randbelow_from_random",
    "string_mass",
    "EnumTree",
    "enumerate_trees",
    "Validation",
    "validate_tree",
]
