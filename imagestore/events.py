"""Dependency-injection seams for fault injection and concurrency control.

Tests either register *faults* (``crash_at``) that raise
:class:`InjectedCrash` at a chosen boundary, or register *hooks* that drive
other operations at a deterministic point.

InjectedCrash derives from :class:`BaseException` so repository cleanup
code (``except Exception``) cannot accidentally swallow a simulated process
crash: the in-memory state is abandoned and the store is reopened with
:meth:`ImageStore.open`, exactly as after a real kill -9.
"""

import threading


class InjectedCrash(BaseException):
    """Raised at an injected crash boundary; ordinary handlers cannot catch."""


_NO_FAULT = object()


# Import boundaries -------------------------------------------------------
IMPORT_STAGED = "import.staged"                  # staged+verified, pre journal
IMPORT_JOURNAL_APPENDED = "import.appended"      # import linearisation point
IMPORT_BEGIN_WINDOW = "import.begin_window"      # begin durable, lock released

# Publish boundaries ------------------------------------------------------
PUB_CHECKED = "publish.checked"                  # closure verified, pre temp
PUB_TEMP_WRITTEN = "publish.temp_written"        # temp manifest durable
PUB_BEFORE_REPLACE = "publish.before_replace"      # record durable, pre rename
PUB_MANIFEST_REPLACED = "publish.replaced"       # publish linearisation point
PUB_CURRENT_APPENDED = "publish.current_appended"

# Lease boundaries --------------------------------------------------------
LEASE_ACQUIRED = "lease.acquired"                # acquire linearisation point
LEASE_RELEASED = "lease.released"                # release linearisation point
LEASE_RECOVERED = "lease.recovered"              # recovery linearisation point

# Garbage collection boundaries ------------------------------------------
GC_MARK_DONE = "gc.mark_done"                    # live set computed
GC_AFTER_MARK_LOCK = "gc.after_mark_lock"        # mark linearisation point
GC_BEFORE_UNLINK = "gc.before_unlink"            # per object, pre unlink
GC_AFTER_UNLINK = "gc.after_unlink"              # per object, post unlink
GC_SWEEP_YIELD = "gc.sweep_yield"                 # between unlinks, lock free


class EventBus:
    """Thread-safe registry of one-shot faults and repeatable hooks."""

    def __init__(self):
        self._cond = threading.Condition()
        self._faults = {}
        self._hooks = {}

    def crash_at(self, event, detail=None):
        """Schedule a crash the next time ``event`` fires."""
        with self._cond:
            self._faults[event] = detail

    def on(self, event, hook):
        """Register a hook called with ``detail`` when ``event`` fires."""
        with self._cond:
            self._hooks.setdefault(event, []).append(hook)

    def once(self, event, hook):
        """Register a hook that runs only on the next firing of ``event``."""

        def wrapper(detail):
            hook(detail)
            self.off(event, wrapper)

        with self._cond:
            self._hooks.setdefault(event, []).append(wrapper)
        return wrapper

    def off(self, event, hook):
        with self._cond:
            try:
                self._hooks.get(event, []).remove(hook)
            except ValueError:
                pass

    def fire(self, event, detail=None):
        """Fire an event: a scheduled fault supersedes every hook."""
        with self._cond:
            fault = self._faults.pop(event, _NO_FAULT)
            hooks = list(self._hooks.get(event, ()))
        if fault != _NO_FAULT:
            raise InjectedCrash(f"crash injected at {event}: {fault!r}")
        for hook in hooks:
            hook(detail)

    def pending_faults(self):
        with self._cond:
            return set(self._faults)
