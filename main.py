#!/usr/bin/env python3
"""Entry point: python3 main.py minimize examples/sample_dfa.json"""

import sys

from dfa_min.cli import main

if __name__ == "__main__":
    sys.exit(main())
