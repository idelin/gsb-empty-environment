"""Parser for version-set specification strings.

Grammar (whitespace is ignored; ``|`` union, ``&`` or adjacency intersection,
``-`` difference with intersection precedence)::

    expr   := term ('|' term)*
    term   := factor (('&' | '-') factor)*
    factor := interval ['@' CHANNEL]
            | ('>=' | '<=' | '>' | '<' | '==' | '!=') version ['@' CHANNEL]
            | version ['@' CHANNEL]
            | '@' CHANNEL

Interval syntax: ``[lo,hi)`` where either endpoint may be ``*`` (unbounded).
Prerelease endpoints look like ``2.0.0-alpha.1``.  A bare release version is
an exact point on the release track; a prerelease point belongs only to its
channel.  ``@alpha`` projects the whole constraint onto that channel and
allows *only* that channel; alone it denotes the full channel universe.
"""
from __future__ import annotations

import re

from .intervals import NEG, POS, IntervalSet
from .version import Version
from .versionset import RELEASE_DOMAIN, VersionSet, pre_domain

_TOKEN_RE = re.compile(
    r"\[[^][]*\)"
    r"|\{\}"
    r"|>=|<=|==|!="
    r"|[<>&|-]"
    r"|@"
    r"|\d+\.\d+\.\d+(?:-[A-Za-z][A-Za-z0-9]*(?:\.\d+)?)?"
    r"|[A-Za-z][A-Za-z0-9]*"
)
_COMPARATORS = {">=", "<=", ">", "<", "==", "!="}


class SpecError(ValueError):
    pass


def parse_spec(text: str) -> VersionSet:
    if "rel=" in text or ";pre:" in text:
        return _parse_canonical(text)
    tokens = _TOKEN_RE.findall(text)
    parser = _Parser(tokens)
    result = parser.parse_expr()
    if parser.peek() is not None:
        raise SpecError(f"unexpected token {parser.peek()!r} in {text!r}")
    return result


def _parse_canonical(text: str) -> VersionSet:
    releases = None
    channels: dict[str, IntervalSet] = {}
    from .versionset import pre_domain

    for part in text.split(";"):
        key, value = part.split("=", 1)
        if key == "rel":
            releases = _parse_interval_series(value, RELEASE_DOMAIN)
        elif key.startswith("pre:"):
            channel = key.split(":", 1)[1]
            channels[channel] = _parse_interval_series(value, pre_domain(channel))
        else:
            raise SpecError(f"unknown lane in canonical form: {key}")
    return VersionSet(releases if releases is not None else IntervalSet.empty(RELEASE_DOMAIN), channels)


def _parse_interval_series(text: str, domain) -> IntervalSet:
    if text == "{}":
        return IntervalSet.empty(domain)
    pieces = []
    for literal in text.split("|"):
        literal = literal.strip()
        assert literal.startswith("[") and literal.endswith(")")
        inner = literal[1:-1]
        lo_s, hi_s = inner.split(",", 1)
        lo = NEG if lo_s == "*" else Version.parse(lo_s)
        hi = POS if hi_s == "*" else Version.parse(hi_s)
        pieces.append((lo, hi))
    return IntervalSet(domain, pieces)


class _Parser:
    def __init__(self, tokens: list[str]):
        self.tokens = tokens
        self.pos = 0

    def peek(self, offset: int = 0):
        idx = self.pos + offset
        return self.tokens[idx] if idx < len(self.tokens) else None

    def take(self) -> str:
        token = self.tokens[self.pos]
        self.pos += 1
        return token

    def expect(self, value: str) -> None:
        if self.peek() != value:
            raise SpecError(f"expected {value!r}, got {self.peek()!r}")
        self.pos += 1

    def parse_expr(self) -> VersionSet:
        result = self.parse_term()
        while self.peek() == "|":
            self.take()
            result = result | self.parse_term()
        return result

    def parse_term(self) -> VersionSet:
        result = self.parse_factor()
        while True:
            token = self.peek()
            if token == "&":
                self.take()
                result = result & self.parse_factor()
            elif token == "-":
                self.take()
                result = result - self.parse_factor()
            else:
                return result

    def parse_factor(self) -> VersionSet:
        token = self.peek()
        if token == "@":
            self.take()
            channel = self._channel_name()
            return VersionSet.pre_universe(channel)
        if token == "{}":
            self.take()
            return VersionSet.empty()
        if token and token.startswith("["):
            return self._interval()
        if token in _COMPARATORS:
            return self._comparator()
        return self._point()

    def _channel_name(self) -> str:
        token = self.peek()
        if not token or not re.match(r"^[A-Za-z][A-Za-z0-9]*$", token):
            raise SpecError(f"expected channel name, got {token!r}")
        return self.take()

    def _optional_channel(self) -> str | None:
        if self.peek() == "@":
            self.take()
            return self._channel_name()
        return None

    def _interval(self) -> VersionSet:
        literal = self.take()[1:]
        if not literal.endswith(")"):
            raise SpecError("intervals must be half-open [lo,hi)")
        literal = literal[:-1]
        if "," not in literal:
            raise SpecError(f"bad interval {literal!r}")
        lo_s, hi_s = (part.strip() for part in literal.split(",", 1))
        lane = self._optional_channel()
        endpoints: list[Version | object] = []
        for part in (lo_s, hi_s):
            if part == "*":
                endpoints.append(NEG if len(endpoints) == 0 else POS)
            else:
                endpoints.append(self._project(Version.parse(part), lane))
        lo, hi = endpoints
        if lane:
            tracks = {lane: IntervalSet(pre_domain(lane), ((lo, hi),))}
            return VersionSet(IntervalSet.empty(RELEASE_DOMAIN), tracks)
        return VersionSet(IntervalSet(RELEASE_DOMAIN, ((lo, hi),)))

    def _comparator(self) -> VersionSet:
        op = self.take()
        version = Version.parse(self.take())
        lane = self._optional_channel()
        value = self._project(version, lane)
        domain = pre_domain(lane) if lane else RELEASE_DOMAIN
        after = domain.successor(value)
        if op == ">=":
            pieces = ((value, POS),)
        elif op == ">":
            pieces = ((after, POS),)
        elif op == "<":
            pieces = ((NEG, value),)
        elif op == "<=":
            pieces = ((NEG, after),)
        elif op == "==":
            pieces = ((value, after),)
        else:
            pieces = None
        track = (
            IntervalSet.universe(domain) - IntervalSet(domain, ((value, after),))
            if pieces is None
            else IntervalSet(domain, pieces)
        )
        if lane:
            return VersionSet(IntervalSet.empty(RELEASE_DOMAIN), {lane: track})
        return VersionSet(track)

    def _point(self) -> VersionSet:
        version = Version.parse(self.take())
        lane = self._optional_channel()
        if version.channel is not None:
            if lane is not None and lane != version.channel:
                raise SpecError("point channel conflicts with @channel")
            lane = version.channel
        value = self._project(version, lane)
        domain = pre_domain(lane) if lane else RELEASE_DOMAIN
        after = domain.successor(value)
        track = IntervalSet(domain, ((value, after),))
        if lane:
            return VersionSet(IntervalSet.empty(RELEASE_DOMAIN), {lane: track})
        return VersionSet(track)

    @staticmethod
    def _project(version: Version, lane: str | None) -> Version:
        if lane is None:
            return version
        if version.channel is None:
            return version.as_prerelease(lane)
        if version.channel != lane:
            raise SpecError(f"endpoint channel {version.channel} != @{lane}")
        return version
