"""regex_engine：显式字母表上的小型正则引擎。

特性：连接/并/星号/字符类/括号，编译为 epsilon-NFA 后按需确定化，
状态预算限制膨胀，带标签捕获，分块输入与最长匹配等待。
不依赖任何现成正则或自动机库（仅标准库）。
"""
from .dfa import Determinizer
from .errors import BudgetExceeded, CompileError, ParseError
from .matcher import Match, Regex, SearchResult, StreamMatcher
from .parser import Parser

__all__ = [
    "Regex", "Match", "SearchResult", "StreamMatcher",
    "Parser", "Determinizer",
    "ParseError", "CompileError", "BudgetExceeded",
]
