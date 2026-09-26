"""Shared import shim so examples run standalone from the repo root."""
import pathlib
import sys

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent.parent))
