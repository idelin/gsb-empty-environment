"""tailcall：带尾调用优化的小型表达式语言。

语法与解释执行均为自行实现，不依赖 eval 或现成编译器；
源码位置一律使用 UTF-8 字节区间。
"""

from .errors import LangError
from .interpreter import Machine, run
from .parser import parse

__all__ = ["LangError", "Machine", "parse", "run"]
