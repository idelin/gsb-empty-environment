import os

from modlang.errors import LangError
from modlang.loader import Loader

HERE = os.path.dirname(os.path.abspath(__file__))


def run(path):
    lines = []
    loader = Loader(out=lines.append)
    try:
        loader.run(path)
    except LangError as err:
        lines.append(f"error: {err}")
    return lines


print("== sample 1: cross-module function cycle (a <-> b) ==")
for line in run(os.path.join(HERE, "examples", "main.mod")):
    print(line)

print()
print("== sample 2: circular read of uninitialized const ==")
for line in run(os.path.join(HERE, "examples", "bad", "main.mod")):
    print(line)
