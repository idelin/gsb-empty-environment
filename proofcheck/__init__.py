"""proofcheck：小型证明步骤格式与独立验证器。

支持四条规则的逐步独立验证：expand（展开）、combine（合并同类项）、
differentiate（求导）、substitute（代入）。所有计算使用 Fraction 做
有理数精确运算，不依赖任何符号代数库。
"""

from .verifier import StepReport, VerificationResult, verify

__all__ = ["StepReport", "VerificationResult", "verify"]
