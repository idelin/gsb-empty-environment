import sys

from .errors import LangError
from .loader import Loader


def main(argv):
    if len(argv) != 1:
        print("usage: python3.11 -m modlang <file.mod>", file=sys.stderr)
        return 2
    loader = Loader()
    try:
        loader.run(argv[0])
    except LangError as err:
        print(f"error: {err}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
