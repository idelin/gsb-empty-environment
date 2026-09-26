#!/bin/sh
# Run the full unittest suite with Python 3.11 (falls back to python3).
cd "$(dirname "$0")"
PY=$(command -v python3.11 || command -v python3)
PYTHONPATH=src "$PY" -m unittest discover -s tests -v
