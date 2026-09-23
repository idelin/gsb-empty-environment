"""运行样例：加载本地夹具，逐事务校验，演示冲突隔离与解决。"""

from txguard import MISSING, TxStatus, load_fixture
from txguard.engine import Engine


def show(result) -> None:
    print(f"  {result.summary()}")
    for conflict in result.conflicts:
        print(f"    op#{conflict.op_index} kind={conflict.kind.value}")
        for f in conflict.fields:
            print(f"      字段 {f.table}.{f.pk}.{f.field}: "
                  f"期望前值={f.expected!r} 实际={f.actual!r}")


def show_state(engine, label) -> None:
    print(f"  [{label}] 状态:")
    for (table, pk), row in sorted(engine.store.snapshot().items()):
        print(f"    {table}/{pk} v{engine.store.version((table, pk))}: {row}")
    print(f"    隔离键: {sorted(engine.quarantined) or '无'}")


def main() -> None:
    engine = Engine()
    txs = load_fixture("fixtures/scenario.json")

    print("== 1. 按提交序号处理夹具事务 ==")
    held_tx = None
    for tx in txs:
        result = engine.submit(tx)
        show(result)
        if result.status is TxStatus.HELD:
            held_tx = tx

    show_state(engine, "冲突隔离后")

    print("\n== 2. 解决隔离键 accounts/A，重试被挂起的事务 ==")
    for result in engine.resolve(("accounts", "A")):
        show(result)
    show_state(engine, "解决后")

    assert MISSING != None  # noqa: E711 缺失字段不等于 null
    print("\n演示完成。")


if __name__ == "__main__":
    main()
