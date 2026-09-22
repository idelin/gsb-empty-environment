"""命令行入口：python3 -m unitlang [文件]（缺省读标准输入）。"""
from __future__ import annotations

import sys

from . import run
from .errors import LangError, format_error


def main() -> None:
    args = sys.argv[1:]
    if args:
        with open(args[0], encoding="utf-8") as f:
            source = f.read()
    else:
        source = sys.stdin.read()
    try:
        for line in run(source):
            print(line)
    except LangError as err:
        print(format_error(source, err), file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    main()
