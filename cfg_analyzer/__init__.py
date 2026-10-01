"""cfg_analyzer：上下文无关文法诊断工具。

- 可生成性最小不动点证书（可独立校验）
- 不可达符号（双独立实现交叉核验）
- ε-环（可空环）与无限歧义诊断，与语言无限性分别判定
- 最短问题链、无用规则清理映射、等价简化
- 规则撤回后的增量修复
- 有界推导树枚举交叉核验（枚举没找到 ≠ 不可生成）
- 可独立执行的反例 / 见证脚本
"""
from .grammar import Grammar, GrammarError, Production
from .report import Diagnosis, analyze, format_report
from .incremental import IncrementalAnalyzer, RetractionDiff

__all__ = [
    "Grammar", "GrammarError", "Production",
    "Diagnosis", "analyze", "format_report",
    "IncrementalAnalyzer", "RetractionDiff",
]
