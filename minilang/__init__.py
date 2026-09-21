"""minilang：手写的小型命令语言 + CFG 数据流分析。

- 词法/语法/解释器全部手写，不用 eval 或现成编译器
- 源码位置一律使用 UTF-8 字节区间 [start, end)
- CFG 上做到达定义（may，并集汇合）与一定已定义（must，交集汇合）分析
- 循环经工作list迭代求不动点；不可达语句单独标记
"""
from .lexer import LexError, Token, tokenize
from .parser import ParseError, Parser
from .cfg import CFGBuilder, CFG, Block
from .analysis import AnalysisResult, Diagnostic, analyze
from .interpreter import InterpError, Interpreter
from .nodes import Program


def parse_source(text: str) -> Program:
    return Parser(tokenize(text)).parse_program()


__all__ = [
    "LexError", "Token", "tokenize",
    "ParseError", "Parser",
    "CFGBuilder", "CFG", "Block",
    "AnalysisResult", "Diagnostic", "analyze",
    "InterpError", "Interpreter",
    "Program", "parse_source",
]
