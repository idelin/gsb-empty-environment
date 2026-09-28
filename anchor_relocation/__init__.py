"""段落锚点重定位组件（仅使用 Python 3.11 标准库）。"""

from .core import (
    AnchorError,
    AnchorResult,
    CollapseEvent,
    Edit,
    EditError,
    Gravity,
    OverlappingEditsError,
    TextHistory,
    apply_edits,
)

__all__ = [
    "AnchorError",
    "AnchorResult",
    "CollapseEvent",
    "Edit",
    "EditError",
    "Gravity",
    "OverlappingEditsError",
    "TextHistory",
    "apply_edits",
]
