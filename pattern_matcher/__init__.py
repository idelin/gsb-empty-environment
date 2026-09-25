"""Small pattern-graph matcher with incremental match maintenance."""
from .graphs import (Edge, PatternEdge, PatternGraph, PatternVertex,
                     TargetGraph, Vertex, match_predicates)
from .matcher import (DedupStrategy, Match, MatchEnumerator, Metrics,
                      StaleVersionError, enumerate_matches,
                      iter_vertex_mappings, mapping_id)
from .engine import Diff, IncrementalMatchEngine

__all__ = [
    "Edge", "PatternEdge", "PatternGraph", "PatternVertex", "TargetGraph",
    "Vertex", "match_predicates",
    "DedupStrategy", "Match", "MatchEnumerator", "Metrics",
    "StaleVersionError", "enumerate_matches", "iter_vertex_mappings",
    "mapping_id",
    "Diff", "IncrementalMatchEngine",
]
