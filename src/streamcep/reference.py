"""Reference matcher: total-order enumeration over the complete, deduplicated
event set, with no watermarks and no late data. It computes the set of final
matches per key and is used to cross-check the streaming engine on scenarios
where all data is on time.
"""

from __future__ import annotations

from collections import defaultdict
from typing import Dict, Iterable, List, Set, Tuple

from .matcher import Event, WINDOW_MS, _sort_key  # type: ignore


def reference_matches(events: Iterable[Event],
                      window_ms: int = WINDOW_MS
                      ) -> Dict[str, Set[Tuple[str, int, str, int]]]:
    """Return ``{key: {(a_source, a_seq, b_source, b_seq), ...}}``.

    The input is deduplicated by (source, seq), sorted in the defined total
    order, and every A-B pair inside the window with no C strictly between
    them is reported.
    """
    unique: Dict[Tuple[str, int], Event] = {}
    for ev in events:
        unique.setdefault(ev.rid, ev)
    ordered = sorted(unique.values(), key=_sort_key)

    by_key: Dict[str, List[Event]] = defaultdict(list)
    for ev in ordered:
        by_key[ev.key].append(ev)

    result: Dict[str, Set[Tuple[str, int, str, int]]] = defaultdict(set)
    for key, evs in by_key.items():
        for i, a in enumerate(evs):
            if a.etype != "A":
                continue
            for j in range(i + 1, len(evs)):
                b = evs[j]
                if b.time - a.time > window_ms:
                    break
                if b.etype != "B":
                    continue
                blocked = any(evs[k].etype == "C" for k in range(i + 1, j))
                if not blocked:
                    result[key].add((a.source, a.seq, b.source, b.seq))
    return result
