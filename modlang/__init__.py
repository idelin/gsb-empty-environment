"""A tiny language with explicit imports/exports and a module initialization graph."""

from .interpreter import Interpreter, LangError

__all__ = ["Interpreter", "LangError"]
