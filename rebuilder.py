"""Background rebuilder: snapshot -> buffer increments -> catch up -> swap."""

from __future__ import annotations

import logging
from typing import Optional

from cdc import ChangeLog, SnapshotSource
from view_store import View, ViewStore

log = logging.getLogger(__name__)


class Rebuilder:
    """Rebuilds a new view from a fixed snapshot while the old one serves.

    Protocol:
      1. Take a fixed snapshot at LSN L0 (base of the new view).
      2. Replay buffered increments with commit_seq > L0 in commit order.
      3. Catch-up loop: if the log tip moved past the new view's watermark,
         keep replaying; only publish when watermark >= tip at swap time.
      4. The swap is atomic; on any failure the old view stays published.
    """

    def __init__(self, store: ViewStore, snapshots: SnapshotSource, changes: ChangeLog):
        self._store = store
        self._snapshots = snapshots
        self._changes = changes

    def rebuild(self) -> Optional[View]:
        """Build and publish a new view. Returns None on failure (old view
        remains available)."""
        try:
            snapshot = self._snapshots.take()
            view = View(version=-1, watermark=snapshot.lsn, tables=snapshot.tables)
            while True:
                for change in self._changes.read_since(view.watermark):
                    view.apply(change)
                # Atomic "caught up?" check + swap. If the log advanced past
                # our watermark, publish fails and we loop to catch up more.
                if self._store.publish_if_caught_up(view, self._changes.tip):
                    log.info(
                        "published view v%d at watermark %d",
                        view.version,
                        view.watermark,
                    )
                    return view
        except Exception:
            log.exception("rebuild failed; old view stays published")
            return None
