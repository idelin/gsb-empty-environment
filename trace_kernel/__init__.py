"""缓存依赖分析内核：从文件访问轨迹生成可靠的缓存依赖证据。"""
from .evidence import Evidence, build_evidence, load_evidence, save_evidence
from .index import ReverseIndex, evidence_keys
from .kernel import INVALID, INSUFFICIENT, VALID, AnalysisKernel, Verdict
from .replayer import oracle_valid, run_task_program
from .trace import TraceLog
from .vfs import FSError, VFS

__all__ = [
    "AnalysisKernel", "Evidence", "FSError", "INVALID", "INSUFFICIENT",
    "ReverseIndex", "TraceLog", "VALID", "VFS", "Verdict",
    "build_evidence", "evidence_keys", "load_evidence", "oracle_valid",
    "run_task_program", "save_evidence",
]
