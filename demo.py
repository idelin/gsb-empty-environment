"""Run sample: background rebuild with atomic switchover.

Timeline:
  1. View v1 is published from a snapshot at LSN 100.
  2. Live increments 101-102 stream in and are applied to v1.
  3. A rebuild starts from the same fixed snapshot (LSN 100); the log already
     buffers 101-104, and 105 arrives mid-rebuild.
  4. The new view catches up to the switch watermark and swaps atomically.
  5. An old reader keeps querying v1; replayed changes are not double counted.
"""

from cdc import ChangeLog, SnapshotSource
from fixtures import late_change, sample_changes, sample_snapshot
from rebuilder import Rebuilder
from view_store import View, ViewStore


def show(tag, view):
    print(f"  [{tag}] view v{view.version} watermark={view.watermark} "
          f"accounts={view.count('accounts')} "
          f"acct1={view.get('accounts', 1)} "
          f"order10={view.get('orders', 10)}")


def main():
    snapshot = sample_snapshot()
    changes = sample_changes()

    # 1. Initial view v1 from the fixed snapshot.
    store = ViewStore(View(version=1, watermark=snapshot.lsn,
                           tables=snapshot.tables))
    log = ChangeLog()
    print("1) initial view published")
    show("init", store.current())

    # 2. Live increments 101-102: appended to the log AND applied live to v1.
    for change in changes[:2]:
        log.append(change)
        store.apply_live(change)
    print("2) live increments 101-102 applied to v1")
    show("live", store.current())

    # 3./4. Rebuild in the "background" from the fixed snapshot at LSN 100.
    # The remaining fixture changes 103-104 are already buffered in the log;
    # 105 arrives while the rebuild is catching up.
    for change in changes[2:]:
        log.append(change)
    fired = []
    def arrive_mid_rebuild():
        if not fired:
            fired.append(True)
            log.append(late_change())
            print("   ... change 105 arrived mid-rebuild")
    log.on_tip_read = arrive_mid_rebuild

    old_reader = store.current()  # reader pins v1 before the switch
    rebuilder = Rebuilder(store, SnapshotSource(snapshot), log)
    new_view = rebuilder.rebuild()
    print("3) rebuild finished, atomic swap done")
    show("new", new_view)

    # 5. Old reader keeps its version; no double count on replay.
    print("4) old reader still on v1:")
    show("old-reader", old_reader)
    dup = changes[2]  # txn-103, already inside v2
    applied = store.apply_live(dup)
    print(f"5) replay of {dup.txn_id} after switch applied? {applied} "
          f"(no double count)")
    show("final", store.current())


if __name__ == "__main__":
    main()
