"""Reconciliation key-conflict interpreter.

Two engines:
  - onetoone:   bipartite 1-1 matching (max count, min time diff, lex ids)
  - splitmerge: split/merge group matching via exact set packing
"""

__all__ = ["model", "onetoone", "splitmerge"]
