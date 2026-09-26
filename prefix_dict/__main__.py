"""Executable command-line interface for the weighted prefix dictionary.

Run with::

    python -m prefix_dict

Commands (one per line, fields separated by runs of whitespace):

    INSERT <key> <weight>   insert/overwrite a key with an integer weight
    DELETE <key>            remove a key; prints DELETED or MISSING
    WEIGHT <key>            exact lookup; prints "<key> <weight>" or MISSING
    TOPK <prefix|-|all> <k> top-k keys by weight desc, then code-point asc;
                            an empty prefix is written as "-" or "all"
    SIZE                    print number of stored keys
    CHECK                   verify radix-tree invariants; prints OK or ERROR
    QUIT / EXIT             end the session

TOPK prints one "<weight>\\t<key>" pair per line followed by "END".
Malformed input prints "ERROR <message>" and the program continues.
The empty *key* is written as a quoted empty token: use INSERT "" 5.
"""

from __future__ import annotations

import shlex
import sys

from .dictionary import InvariantError, PrefixDictionary


def _parse_tokens(line: str) -> list[str]:
    tokens = shlex.split(line, posix=True)
    if not tokens:
        return []
    return tokens


def run(stdin, stdout) -> int:
    dic = PrefixDictionary()

    def emit(text: str = "") -> None:
        print(text, file=stdout, flush=True)

    for raw in stdin:
        line = raw.strip()
        if not line:
            continue
        try:
            tokens = _parse_tokens(line)
        except ValueError as exc:
            emit(f"ERROR bad quoting: {exc}")
            continue
        if not tokens:
            continue
        command = tokens[0].upper()
        args = tokens[1:]

        if command in ("QUIT", "EXIT"):
            break
        try:
            if command == "INSERT" and len(args) == 2:
                key, weight_text = args
                try:
                    weight = int(weight_text)
                except ValueError:
                    emit("ERROR weight must be an integer")
                    continue
                dic.insert(key, weight)
                emit(f"INSERTED {key} {weight}")
            elif command == "DELETE" and len(args) == 1:
                emit("DELETED " + args[0] if dic.delete(args[0]) else "MISSING")
            elif command == "WEIGHT" and len(args) == 1:
                weight = dic.weight_of(args[0])
                if weight is None:
                    emit("MISSING")
                else:
                    emit(f"{args[0]} {weight}")
            elif command == "TOPK" and len(args) == 2:
                prefix_token, k_text = args
                if prefix_token in ("-", "all"):
                    prefix = ""
                else:
                    prefix = prefix_token
                try:
                    k = int(k_text)
                except ValueError:
                    emit("ERROR k must be an integer")
                    continue
                if k < 0:
                    emit("ERROR k must be non-negative")
                    continue
                for key, weight in dic.prefix_topk(prefix, k):
                    emit(f"{weight}\t{key}")
                emit("END")
            elif command == "SIZE" and not args:
                emit(str(len(dic)))
            elif command == "CHECK" and not args:
                try:
                    dic.check_invariants()
                    emit("OK")
                except InvariantError as exc:
                    emit(f"ERROR {exc}")
            else:
                emit(f"ERROR unknown or malformed command: {line}")
        except (TypeError, ValueError) as exc:
            emit(f"ERROR {exc}")
    return 0


def main() -> int:
    return run(sys.stdin, sys.stdout)


if __name__ == "__main__":
    raise SystemExit(main())
