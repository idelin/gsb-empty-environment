"""带条件的依赖暴露分析器（Python 3.11+ 标准库）。"""

from .analyzer import (
    Conflict,
    ExposureAnalyzer,
    ExposureRecord,
    SupplyPath,
    UpdateReport,
)
from .bdd import BDDManager
from .model import BUILD, PRIVATE, PUBLIC, Edge, Package, PackageGraph, Source

__all__ = [
    "Conflict", "ExposureAnalyzer", "ExposureRecord", "SupplyPath",
    "UpdateReport", "BDDManager",
    "BUILD", "PRIVATE", "PUBLIC", "Edge", "Package", "PackageGraph", "Source",
]
