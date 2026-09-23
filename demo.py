"""运行夹具变更时间线，逐事务打印左外连接视图的增量发布与当前视图。

用法: python3 demo.py
"""
from __future__ import annotations

from collections import defaultdict

from fixtures import changelog
from view import Change, LeftOuterJoinView, Publication, TupleRow


def _fmt(tup: TupleRow) -> str:
    d = dict(tup)
    if d["matched"]:
        return f"{d['l_dname']!s:<18} | {d['r_ename']!s:<8}"
    return f"{d['l_dname']!s:<18} | <NULL>"


def _lines(pub: Publication) -> list[str]:
    out: list[str] = []
    for tup in pub.inserted:
        out.append(f"      + {_fmt(tup)}")
    for tup in pub.deleted:
        out.append(f"      - {_fmt(tup)}")
    if not out:
        out.append("      (净增量为空，无行发布)")
    return out


def main() -> None:
    engine = LeftOuterJoinView(
        join_key="dept_id", left_table="departments", right_table="employees"
    )

    by_txn: dict[str, list[Change]] = defaultdict(list)
    for ch in changelog():
        by_txn[ch.txn_id].append(ch)

    print("=" * 46)
    print(" 增量左外连接视图：departments LEFT JOIN employees")
    print("=" * 46)
    for txn_id in sorted(by_txn, key=lambda t: by_txn[t][0].commit_seq):
        changes = by_txn[txn_id]
        seq = changes[0].commit_seq
        desc = "; ".join(
            f"{ch.table.rstrip('s')}:{ch.pk} "
            f"{'INS' if ch.before is None else 'DEL' if ch.after is None else 'UPD'}"
            for ch in changes
        )
        print(f"\n[seq={seq}] 事务 {txn_id}  ({len(changes)} 条变更) {desc}")
        pub = engine.commit_txn(changes)
        print("\n".join(_lines(pub)))

    print("\n" + "-" * 46)
    print(" 最终视图（按重数展开，共 %d 行）" % len(engine.rows()))
    print("-" * 46)
    for tup in engine.rows():
        print("   " + _fmt(tup))


if __name__ == "__main__":
    main()
