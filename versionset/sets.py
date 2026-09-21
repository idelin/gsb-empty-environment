"""Version sets: symbolic unions of interval/channel/target components.

A :class:`VersionSet` is a finite union of *components*.  A component is
the conjunction of

* a sorted tuple of disjoint ``Interval`` objects over versions,
* a ``LabelSet`` of allowed prerelease channels (``stable`` included only
  when stable releases are allowed), and
* a ``LabelSet`` of allowed target conditions.

Membership of a version ``v`` under target ``t`` requires all three to
match in one component.  Because ``A - B`` for a conjunction is the union
of the per-dimension differences, difference is exact and symbolic: the
infinite version domain is never enumerated.  The normal form merges
components that share the same channel/target labels and drops absorbed
components; semantic equality is decided via the symmetric difference.
"""

from __future__ import annotations

import hashlib
import json

from .intervals import Interval, normalize_intervals, subtract_intervals
from .labels import LabelSet
from .version import KNOWN_CHANNELS, PRE_MIN, STABLE, MIN_VERSION, Version

DEFAULT_CHANNELS = LabelSet.of({STABLE})


class Component:
    __slots__ = ("intervals", "channels", "targets")

    def __init__(self, intervals, channels=None, targets=None):
        self.intervals = normalize_intervals(intervals)
        self.channels = _coerce_labels(channels, DEFAULT_CHANNELS)
        self.targets = _coerce_labels(targets, LabelSet.all())

    def is_empty(self) -> bool:
        return (not self.intervals
                or self.channels.is_empty()
                or self.targets.is_empty())

    def contains(self, v: Version, target: str | None = None) -> bool:
        if not self.channels.contains(v.label):
            return False
        if target is None:
            if not self.targets.is_all():
                return False
        elif not self.targets.contains(target):
            return False
        return any(iv.contains(v) for iv in self.intervals)

    def intersect(self, other: "Component") -> "Component | None":
        ivs = []
        for a in self.intervals:
            for b in other.intervals:
                iv = a.intersect(b)
                if iv is not None:
                    ivs.append(iv)
        comp = Component(ivs, self.channels.intersect(other.channels),
                         self.targets.intersect(other.targets))
        return None if comp.is_empty() else comp

    def difference(self, other: "Component") -> list["Component"]:
        """self minus other, exact: split along the three dimensions."""
        out = []
        rest = subtract_intervals(self.intervals, other.intervals)
        if rest:
            out.append(Component(rest, self.channels, self.targets))
        ch = self.channels.difference(other.channels)
        if not ch.is_empty():
            out.append(Component(self.intervals, ch, self.targets))
        tg = self.targets.difference(other.targets)
        if not tg.is_empty():
            out.append(Component(self.intervals, self.channels, tg))
        return [c for c in out if not c.is_empty()]

    def absorbs(self, other: "Component") -> bool:
        """True if every point of ``other`` is already in ``self``."""
        if not other.channels.is_subset(self.channels):
            return False
        if not other.targets.is_subset(self.targets):
            return False
        for iv in other.intervals:
            if not any(_interval_covers(s, iv) for s in self.intervals):
                return False
        return True

    def key(self) -> str:
        return json.dumps(self.to_dict(), sort_keys=True)

    def to_dict(self):
        return {
            "intervals": [str(iv) for iv in self.intervals],
            "channels": self.channels.to_dict(),
            "targets": self.targets.to_dict(),
        }

    def __eq__(self, other):
        return isinstance(other, Component) and self.key() == other.key()

    def __hash__(self):
        return hash(self.key())

    def __repr__(self):
        return f"Component({self.key()})"


def _interval_covers(big: Interval, small: Interval) -> bool:
    """True if every version of ``small`` also lies in ``big``."""
    if small.low is None:
        if big.low is not None:
            return False
    elif big.low is not None:
        if big.low > small.low:
            return False
        if big.low == small.low and small.low_closed and not big.low_closed:
            return False
    if small.high is None:
        if big.high is not None:
            return False
    elif big.high is not None:
        if big.high < small.high:
            return False
        if big.high == small.high and small.high_closed \
                and not big.high_closed:
            return False
    return True


class VersionSet:
    """A canonical finite union of components."""

    __slots__ = ("components",)

    def __init__(self, components=()):
        self.components = _normalize(components)

    # -- constructors ---------------------------------------------------
    @classmethod
    def empty(cls) -> "VersionSet":
        return cls(())

    @classmethod
    def full(cls) -> "VersionSet":
        return cls([Component([Interval(None, False, None, False)],
                              LabelSet.all(), LabelSet.all())])

    @classmethod
    def interval(cls, low=None, high=None, low_closed=True, high_closed=False,
                 channels=None, targets=None) -> "VersionSet":
        iv = Interval(low, low_closed, high, high_closed)
        return cls([Component([iv], channels, targets)])

    @classmethod
    def point(cls, v: Version, channels=None, targets=None) -> "VersionSet":
        ch = channels if channels is not None else LabelSet.of({v.label})
        return cls([Component([Interval(v, True, v, True)], ch, targets)])

    @classmethod
    def parse(cls, spec: str, channels=None, targets=None) -> "VersionSet":
        """Parse a range spec: ``||`` unions of comma-separated conjunctions
        of ``[a,b]`` intervals, ``>=a``/``<b`` comparators or bare versions."""
        result = cls.empty()
        for alt in spec.split("||"):
            conj = cls.full() if channels is None and targets is None else \
                cls([Component([Interval(None, False, None, False)],
                               channels, targets)])
            for term in _split_terms(alt):
                term = term.strip()
                if not term:
                    continue
                conj = conj.intersect(
                    cls._parse_term(term, channels, targets))
            result = result.union(conj)
        return result

    @classmethod
    def _parse_term(cls, term: str, channels, targets) -> "VersionSet":
        term = term.strip()
        if term.startswith(("[", "(")):
            closed_lo = term[0] == "["
            closed_hi = term[-1] == "]"
            body = term[1:-1]
            lo_s, hi_s = body.split(",", 1)
            low = Version.parse(lo_s) if lo_s.strip() else None
            high = Version.parse(hi_s) if hi_s.strip() else None
            return cls.interval(low, high, closed_lo, closed_hi,
                                channels, targets)
        for op, clo, chi in ((">=", True, False), ("<=", False, True),
                             (">", False, False), ("<", False, False),
                             ("=", True, True)):
            if term.startswith(op):
                v = Version.parse(term[len(op):])
                if op in (">=", ">"):
                    return cls.interval(v, None, clo, False, channels, targets)
                if op in ("<=", "<"):
                    return cls.interval(None, v, False, chi, channels, targets)
                return cls.point(v, channels, targets)
        return cls.point(Version.parse(term), channels, targets)

    # -- algebra ----------------------------------------------------------
    def union(self, other: "VersionSet") -> "VersionSet":
        return VersionSet(self.components + other.components)

    def intersect(self, other: "VersionSet") -> "VersionSet":
        out = []
        for a in self.components:
            for b in other.components:
                comp = a.intersect(b)
                if comp is not None:
                    out.append(comp)
        return VersionSet(out)

    def difference(self, other: "VersionSet") -> "VersionSet":
        comps = list(self.components)
        for b in other.components:
            nxt = []
            for a in comps:
                nxt.extend(a.difference(b))
            comps = nxt
        return VersionSet(comps)

    # -- queries ----------------------------------------------------------
    def is_empty(self) -> bool:
        return not self.components

    def contains(self, v: Version | str, target: str | None = None) -> bool:
        if isinstance(v, str):
            v = Version.parse(v)
        return any(c.contains(v, target) for c in self.components)

    def min_version(self, target: str | None = None) -> Version | None:
        """Smallest version in the set (None when empty)."""
        best = None
        for comp in self.components:
            if target is None:
                if not comp.targets.is_all():
                    continue
            elif not comp.targets.contains(target):
                continue
            for iv in comp.intervals:
                lo = MIN_VERSION if iv.low is None else \
                    (iv.low if iv.low_closed else iv.low.successor())
                cand = _ceil_to_channels(lo, comp.channels)
                if cand is not None and iv.contains(cand):
                    if best is None or cand < best:
                        best = cand
        return best

    def counterexample(self, other: "VersionSet",
                       target: str | None = None) -> Version | None:
        """Smallest version in the symmetric difference, or None if equal."""
        diff = self.difference(other).union(other.difference(self))
        return diff.min_version(target)

    # -- equality / hashing -------------------------------------------------
    def __eq__(self, other):
        if not isinstance(other, VersionSet):
            return NotImplemented
        return self.counterexample(other) is None

    def __hash__(self):
        return hash(self.canonical())

    def canonical(self) -> str:
        """Canonical serialization: order-independent JSON of the normal form."""
        return json.dumps(self.to_dict(), sort_keys=True)

    def digest(self) -> str:
        return hashlib.sha256(self.canonical().encode()).hexdigest()

    def to_dict(self):
        return {"components": [c.to_dict() for c in self.components]}

    @classmethod
    def from_dict(cls, data) -> "VersionSet":
        comps = []
        for c in data.get("components", []):
            ivs = [_parse_interval_str(s) for s in c["intervals"]]
            comps.append(Component(ivs, LabelSet.from_dict(c["channels"]),
                                   LabelSet.from_dict(c["targets"])))
        return cls(comps)

    def __repr__(self):
        return f"VersionSet({self.canonical()})"


# -- internal helpers -----------------------------------------------------

def _coerce_labels(value, default):
    if value is None:
        return default
    if isinstance(value, LabelSet):
        return value
    return LabelSet.of(value)


def _split_terms(alt: str) -> list[str]:
    """Split a conjunction on top-level commas (not inside brackets)."""
    terms, depth, cur = [], 0, []
    for ch in alt:
        if ch in "[(":
            depth += 1
        elif ch in ")]":
            depth -= 1
        if ch == "," and depth == 0:
            terms.append("".join(cur))
            cur = []
        else:
            cur.append(ch)
    terms.append("".join(cur))
    return terms


def _normalize(components) -> tuple[Component, ...]:
    # Merge components that share identical channel/target labels.
    by_labels: dict[tuple, list] = {}
    order: list[tuple] = []
    for comp in components:
        if comp.is_empty():
            continue
        key = (comp.channels, comp.targets)
        if key not in by_labels:
            by_labels[key] = []
            order.append(key)
        by_labels[key].extend(comp.intervals)
    merged = [Component(ivs, key[0], key[1]) for key, ivs in by_labels.items()]
    # Drop components fully absorbed by another component.
    kept = []
    for i, comp in enumerate(merged):
        if any(i != j and other.absorbs(comp)
               for j, other in enumerate(merged)):
            continue
        kept.append(comp)
    kept.sort(key=lambda c: c.key())
    return tuple(kept)


def _allowed_prerelease_channels(channels: LabelSet) -> list[str]:
    """Allowed prerelease channels in ascending version order."""
    if channels.include is None:
        out = [c for c in KNOWN_CHANNELS if c not in channels.exclude]
        if not out:
            out = ["a"]  # universe is unbounded; 'a' is the smallest channel
        return out
    pres = [c for c in channels.include if c != STABLE]
    pres.sort(key=lambda c: (Version(0, 0, 0, c, PRE_MIN)._key()))
    return pres


def _ceil_to_channels(v: Version, channels: LabelSet) -> Version | None:
    """Smallest version >= v whose channel label is allowed."""
    if channels.is_empty():
        return None
    stable_ok = channels.contains(STABLE)
    pres = _allowed_prerelease_channels(channels)
    if v.channel is not None:
        if channels.contains(v.channel):
            return v
        later = [c for c in pres
                 if Version(0, 0, 0, c, PRE_MIN)._key() >
                 Version(0, 0, 0, v.channel, PRE_MIN)._key()]
        if later:
            return Version(v.major, v.minor, v.patch, later[0], PRE_MIN)
        if stable_ok:
            return Version(v.major, v.minor, v.patch)
        nxt = v.next_triple()
        if pres:
            return Version(*nxt, pres[0], PRE_MIN)
        return Version(*nxt)
    if stable_ok:
        return v
    nxt = v.next_triple()
    if pres:
        return Version(*nxt, pres[0], PRE_MIN)
    return Version(*nxt)


def _parse_interval_str(text: str) -> Interval:
    closed_lo = text[0] == "["
    closed_hi = text[-1] == "]"
    lo_s, hi_s = text[1:-1].split(",", 1)
    low = None if lo_s.strip() == "-inf" else Version.parse(lo_s)
    high = None if hi_s.strip() == "+inf" else Version.parse(hi_s)
    return Interval(low, closed_lo, high, closed_hi)
