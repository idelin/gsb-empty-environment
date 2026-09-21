#!/bin/sh
# Run the full unittest suite (Python 3.11+).
PYTHONPATH=src python3 -m unittest discover -s tests -v
