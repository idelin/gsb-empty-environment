import sys

from .runner import run_file

if __name__ == "__main__":
    if len(sys.argv) != 2:
        print("usage: python -m rill <file.rec>", file=sys.stderr)
        raise SystemExit(2)
    run_file(sys.argv[1])
