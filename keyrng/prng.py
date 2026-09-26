"""Explicit key-driven PRNG (JAX-style, stateless).

A random key is an immutable pair of 64-bit unsigned words.  There is no
global or mutable random state anywhere in this library: every random
stream is a pure function of the key (and of an explicit counter), so

* the same key always produces the same values,
* substreams derived from different keys are independent of the order in
  which they are consumed,
* splitting a key is the only way to obtain new keys.

Bit-mixing construction (documented so results can be validated
independently):

    mix64(x):  the SplitMix64 finaliser (Stafford / Vigna)
        x ^= x >> 30; x *= 0xBF58476D1CE4E5B9
        x ^= x >> 27; x *= 0x94D049BB133111EB
        x ^= x >> 31
    (all arithmetic mod 2**64)

    random_bits(key, i) = mix64(key.a ^ mix64(key.b + i * GOLDEN))
        where GOLDEN = 0x9E3779B97F4A7C15 (fractional golden ratio, 2**64)

    fold_in(key, d) = (mix64(key.a ^ mix64(d)), mix64(key.b + d * GOLDEN))

    split(key, n) derives subkey j from random_bits(key, 2j) and
    random_bits(key, 2j + 1).

String/int identifiers (node names, sample ids) are hashed with 64-bit
FNV-1a, which is stable across processes (unlike builtins.hash).
"""

from __future__ import annotations

MASK64 = (1 << 64) - 1
GOLDEN = 0x9E3779B97F4A7C15
_FNV_OFFSET = 0xCBF29CE484222325
_FNV_PRIME = 0x100000001B3


def mix64(x: int) -> int:
    """SplitMix64 finaliser: a bijective 64-bit bit mixer."""
    x &= MASK64
    x ^= x >> 30
    x = (x * 0xBF58476D1CE4E5B9) & MASK64
    x ^= x >> 27
    x = (x * 0x94D049BB133111EB) & MASK64
    x ^= x >> 31
    return x & MASK64


def fnv1a64(data: bytes) -> int:
    """Stable 64-bit FNV-1a hash of a byte string."""
    h = _FNV_OFFSET
    for byte in data:
        h ^= byte
        h = (h * _FNV_PRIME) & MASK64
    return h


def hash_data(data) -> int:
    """Hash an int or str identifier into 64 bits, stably."""
    if isinstance(data, int):
        return mix64(data)
    if isinstance(data, str):
        return fnv1a64(data.encode("utf-8"))
    raise TypeError(f"cannot hash {type(data)!r} into a key")


class PRNGKey:
    """An immutable explicit random key: two 64-bit words."""

    __slots__ = ("a", "b")

    def __init__(self, a: int, b: int):
        self.a = a & MASK64
        self.b = b & MASK64

    @property
    def data(self) -> tuple[int, int]:
        return (self.a, self.b)

    def __eq__(self, other) -> bool:
        return isinstance(other, PRNGKey) and self.data == other.data

    def __hash__(self) -> int:
        return hash(self.data)

    def __repr__(self) -> str:
        return f"PRNGKey(0x{self.a:016x}, 0x{self.b:016x})"


def key(seed: int) -> PRNGKey:
    """Create a root key from a non-negative integer seed."""
    if not isinstance(seed, int) or seed < 0:
        raise ValueError("seed must be a non-negative int")
    return PRNGKey(mix64(seed), mix64(seed ^ GOLDEN))


def fold_in(k: PRNGKey, data) -> PRNGKey:
    """Derive a child key by folding in an integer/string identifier."""
    d = hash_data(data)
    return PRNGKey(mix64(k.a ^ mix64(d)), mix64((k.b + d * GOLDEN) & MASK64))


def split(k: PRNGKey, num: int = 2) -> tuple[PRNGKey, ...]:
    """Split a key into ``num`` independent subkeys."""
    if num < 1:
        raise ValueError("num must be >= 1")
    bits = random_bits(k, 2 * num)
    return tuple(PRNGKey(bits[2 * j], bits[2 * j + 1]) for j in range(num))


def random_bits(k: PRNGKey, count: int) -> list[int]:
    """Return ``count`` 64-bit words; a pure function of (key, counter).

    The i-th word depends only on the key and i, never on how many words
    were drawn before, so streams are state-independent.
    """
    if count < 0:
        raise ValueError("count must be >= 0")
    return [mix64(k.a ^ mix64((k.b + i * GOLDEN) & MASK64)) for i in range(count)]


class KeyCollisionError(RuntimeError):
    """Raised in strict mode when a key is consumed more than once."""


class KeyRegistry:
    """Optional strict detector for repeated key consumption."""

    def __init__(self) -> None:
        self._seen: set[tuple[int, int]] = set()

    def register(self, k: PRNGKey) -> PRNGKey:
        if k.data in self._seen:
            raise KeyCollisionError(f"random key consumed twice: {k!r}")
        self._seen.add(k.data)
        return k

    def __len__(self) -> int:
        return len(self._seen)
