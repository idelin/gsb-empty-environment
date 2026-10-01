"""cfgdoctor: CFG 无用规则 / epsilon 环 / 增量修复分析器(纯标准库)。"""
from .bounded_enum import CrossCheckReport, cross_check, enumerate_terminal_strings
from .chains import ProblemChain, shortest_problem_chains
from .cycles import (EpsilonCycle, Infiniteness, analyze_language_infiniteness,
                     find_epsilon_cycles)
from .generating import (GeneratingCertificate, NullableInfo,
                         compute_generating, compute_nullable)
from .grammar import Grammar, Production, format_symbol_string, sub_grammar
from .incremental import ChangeSet, IncrementalAnalyzer
from .reachability import compute_reachable
from .report import Report, analyze, format_report
from .simplify import CleanupEntry, Simplification, simplify
from .verify import (DerivationError, certificate_to_derivation,
                     execute_certificate_witness, independent_reachable,
                     run_derivation, verify_cycle_witness,
                     verify_generating_certificate)

__all__ = [
    "Grammar", "Production", "format_symbol_string", "sub_grammar",
    "GeneratingCertificate", "NullableInfo", "compute_generating",
    "compute_nullable", "compute_reachable",
    "EpsilonCycle", "Infiniteness", "find_epsilon_cycles",
    "analyze_language_infiniteness",
    "ProblemChain", "shortest_problem_chains",
    "CleanupEntry", "Simplification", "simplify",
    "ChangeSet", "IncrementalAnalyzer",
    "CrossCheckReport", "cross_check", "enumerate_terminal_strings",
    "Report", "analyze", "format_report",
    "DerivationError", "run_derivation", "independent_reachable",
    "verify_generating_certificate", "certificate_to_derivation",
    "execute_certificate_witness", "verify_cycle_witness",
]
