"""低浪费切割计划：有限规格、切缝、有限库存、余料跨批次复用、精确分支搜索。"""

from .inventory import BatchResult, CampaignResult, Inventory, run_batches
from .model import (
    BarCut,
    Demand,
    SolveResult,
    Status,
    StockSpec,
    cut_metrics,
    piece_fits,
    to_fraction,
)
from .solver import Solver, solve
from .verify import expected_remnants, verify_plan

__all__ = [
    "BarCut",
    "BatchResult",
    "CampaignResult",
    "Demand",
    "Inventory",
    "SolveResult",
    "Solver",
    "Status",
    "StockSpec",
    "cut_metrics",
    "expected_remnants",
    "piece_fits",
    "run_batches",
    "solve",
    "to_fraction",
    "verify_plan",
]
