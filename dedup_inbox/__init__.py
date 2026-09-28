"""Bounded replay-deduplicating inbox with exactly-once effect.

Messages are identified by ``(sender, seq)`` where ``seq`` is a per-sender,
strictly increasing integer. Messages may arrive out of order, but only within
a bounded window ``W`` of the committed cursor ``C``:

* ``seq <= C``                 -> idempotent duplicate, ignored
* ``C < seq <= C + W``         -> cached (and committed immediately when it
                                  extends the contiguous prefix)
* ``seq > C + W``              -> explicitly rejected, cursor never advances

Business state (per-sender accumulated value) and the dedup cursor are
committed in the same SQLite transaction, so a crash before commit leaves no
partial effect while a crash after commit leaves the full effect.
"""

from .inbox import BoundedInbox, DeliveryResult

__all__ = ["BoundedInbox", "DeliveryResult"]
