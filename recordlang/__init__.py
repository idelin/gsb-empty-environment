"""recordlang: records with width subtyping and explicit ro/rw variance."""

from .api import parse_program, run_program, typecheck_program
from .errors import LangError

__all__ = ["parse_program", "run_program", "typecheck_program", "LangError"]
