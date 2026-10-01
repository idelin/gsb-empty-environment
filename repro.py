#!/usr/bin/env python3.11
"""独立反例复现器：对同一输入分别运行主引擎与参考实现并比对。

用法: python3.11 repro.py '<模式>' '<字母表>' '<文本>'
退出码: 0 = 一致；1 = 不一致或主引擎未完成（即反例成立）。
"""
import sys

from regex_engine import Regex
from regex_engine.reference import ref_findall


def main(argv):
    if len(argv) != 4:
        print(__doc__)
        return 2
    pattern, alphabet, text = argv[1], argv[2], argv[3]
    rx = Regex(pattern, alphabet)
    res = rx.findall(text)
    engine = [(m.start, m.end, m.captures) for m in res.matches]
    ref = ref_findall(rx.ast, text, rx.alphabet)
    print(f"模式   : {pattern!r}")
    print(f"字母表 : {alphabet!r}")
    print(f"文本   : {text!r}")
    print(f"主引擎 : {engine}  incomplete={res.incomplete}")
    print(f"参考   : {ref}")
    ok = (not res.incomplete) and engine == ref
    print("结论   :", "一致" if ok else "不一致（反例）")
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main(sys.argv))
