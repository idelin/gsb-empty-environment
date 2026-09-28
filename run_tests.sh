#!/bin/sh
# Run the full unittest suite. Requires Python 3.11.
set -e
cd "$(dirname "$0")"
python3.11 -m unittest discover -s tests -v
