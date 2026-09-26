"""运行样例：python3 demo.py"""

from minisearch import And, Engine, Fuzzy, Index, Not, Or, Phrase, Term


def show(title, r):
    print(f"\n== {title} ==")
    print(f"  incomplete={r.incomplete}  total_hits={r.total_hits}")
    print(f"  hits={r.hits[:6]}{' ...' if len(r.hits) > 6 else ''}")
    s = r.stats
    print(f"  stats: estimate={s.estimate} ops_used={s.ops_used}/{s.max_ops}"
          f" limit_applied={s.limit_applied} elapsed={s.elapsed_ms:.2f}ms")
    if s.reason:
        print(f"  reason: {s.reason}")


def main():
    idx = Index()
    idx.add_document("d1", "the quick brown fox jumps over the lazy dog")
    idx.add_document("d2", "the quick blue hare runs fast")
    idx.add_document("d3", "机器学习是人工智能的分支")
    idx.add_document("d4", "深度学习是机器学习的分支")
    idx.add_document("d5", "quick quick quick x brown")
    for i in range(60):
        idx.add_document(f"bulk{i:02d}", f"alpha beta gamma record {i}")
    for i in range(30):
        idx.add_document(f"rep{i:02d}", "x x x x x")
    engine = Engine(idx)

    show("布尔: quick AND (fox OR hare)",
         engine.search(And(Term("quick"), Or(Term("fox"), Term("hare")))))
    show("短语: 机器学习（CJK unigram 相邻）",
         engine.search(Phrase(["机", "器", "学", "习"])))
    show("模糊: quik (编辑距离<=1)",
         engine.search(Fuzzy("quik", max_edits=1)))
    show("正常 limit 截取: alpha, limit=3（total_hits 仍精确）",
         engine.search(Term("alpha"), limit=3))
    show("宽泛 NOT 受控: NOT alpha, max_ops=10（估算拒绝）",
         engine.search(Not(Term("alpha")), max_ops=10))
    show("同一 NOT 预算充足: max_ops=100000",
         engine.search(Not(Term("alpha")), max_ops=100_000))
    show("子查询共享预算(估算合并): alpha AND beta, max_ops=200",
         engine.search(And(Term("alpha"), Term("beta")), max_ops=200))
    show("对照: 同一查询预算充足",
         engine.search(And(Term("alpha"), Term("beta")), max_ops=100_000))
    show("执行中耗尽: 短语 x x 于高词频文档, max_ops=300",
         engine.search(Phrase(["x", "x"]), max_ops=300))
    show("对照: 同一短语预算充足",
         engine.search(Phrase(["x", "x"]), max_ops=100_000))


if __name__ == "__main__":
    main()
