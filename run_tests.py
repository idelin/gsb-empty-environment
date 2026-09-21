#!/usr/bin/env python3
"""Run the full unittest suite using only the Python 3.11 standard library."""

import sys
import unittest

if sys.version_info < (3, 11):
    raise SystemExit("Python 3.11+ is required")

loader = unittest.TestLoader()
suite = loader.discover("tests")
result = unittest.TextTestRunner(verbosity=2).run(suite)
sys.exit(0 if result.wasSuccessful() else 1)
