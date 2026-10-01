#!/usr/bin/env python3.11
"""运行样例：覆盖任务要求的各项特性。"""
from regex_engine import BudgetExceeded, Regex


def show(title):
    print(f"\n=== {title} ===")


show("1. 解析与源码位置")
rx = Regex("(a|b)*abb", "ab")
print("AST 根节点 span:", rx.ast.span)
print("NFA 状态数:", rx.nfa.n_states)

show("2. 带标签捕获 + 确定优先规则（最左-最长，同长先写分支优先）")
for pat, text in [("(a|ab)(b?)", "ab"), ("(ab|a)(b)", "ab"),
                  ("(a*)(a*)", "aa"), ("(x|y)*(x)(y)", "xxy")]:
    m = Regex(pat, "ab" if "x" not in pat else "xy").search(text)
    print(f"{pat!r} on {text!r}: 匹配{m.span()} 捕获{m.captures}")

show("3. 空串与空语言严格区分")
print("()  在 'ab' 上:", [(m.start, m.end) for m in Regex("()", "ab").findall("ab")])
print("[]  在 'ab' 上:", Regex("[]", "ab").findall("ab").matches)
print("[]* 在 ''  上:", Regex("[]*", "ab").search("").span())

show("4. 嵌套星号不死循环")
for pat in ["(a*)*", "((a*))*", "(a|)*", "(()*)*"]:
    res = Regex(pat, "ab").findall("aa")
    print(f"{pat}: {[(m.start, m.end) for m in res]} incomplete={res.incomplete}")

show("5. 分块输入与最长匹配等待")
s = Regex("a*", "ab").stream()
for chunk in ["aa", "a", "b"]:
    got = s.feed(chunk)
    print(f"feed({chunk!r}) -> {[(m.start, m.end) for m in got]}  (空列表=等待更多输入)")
print("finish() ->", [(m.start, m.end) for m in s.finish()])

show("6. 状态预算：超限标未完成，不返回错误自动机")
pat = "(a|b)*a(a|b)(a|b)(a|b)(a|b)(a|b)"
text = "abbaabba"
ok = Regex(pat, "ab").findall(text)
tight = Regex(pat, "ab", max_states=8).findall(text)
print(f"预算充足: {[(m.start, m.end) for m in ok]} incomplete={ok.incomplete}")
print(f"预算=8  : {[(m.start, m.end) for m in tight]} incomplete={tight.incomplete}")
try:
    Regex("ab", "ab", max_states=1).search("ab")
except BudgetExceeded as e:
    print("search 抛出:", e)

show("7. 缓存驱逐不改变语义（cache_size=1 与默认结果一致）")
corpus = [("(a|b)*abb", "aababbab"), ("(a*)(b|a)*", "aabbaab"),
          ("(x|y)*(x)(y)", "xyxxy")]
for pat, text in corpus:
    alphabet = "xy" if "x" in pat else "ab"
    big = Regex(pat, alphabet).findall(text)
    tiny = Regex(pat, alphabet, cache_size=1).findall(text)
    same = ([(m.start, m.end, m.captures) for m in big]
            == [(m.start, m.end, m.captures) for m in tiny])
    print(f"{pat!r}: 大缓存 == 缓存=1 ? {same}")
