"""Versioned frequency-band rules."""

from dataclasses import dataclass


@dataclass(frozen=True)
class Band:
    """A single named band [lo, hi) in Hz. Bands may nest and overlap."""

    name: str
    lo: float
    hi: float

    def __post_init__(self):
        if not self.lo < self.hi:
            raise ValueError(f"band {self.name!r}: require lo < hi")
        if self.lo < 0:
            raise ValueError(f"band {self.name!r}: lo must be >= 0")


@dataclass(frozen=True)
class BandSet:
    """An immutable, versioned collection of band rules."""

    version: str
    bands: tuple

    def __post_init__(self):
        object.__setattr__(self, "bands", tuple(self.bands))
        names = [b.name for b in self.bands]
        if len(set(names)) != len(names):
            raise ValueError("band names must be unique within a BandSet")
        if not self.version:
            raise ValueError("BandSet.version must be non-empty")

    def to_dict(self):
        return {
            "version": self.version,
            "bands": [{"name": b.name, "lo": b.lo, "hi": b.hi} for b in self.bands],
        }

    @classmethod
    def from_dict(cls, data):
        return cls(
            version=data["version"],
            bands=tuple(Band(b["name"], b["lo"], b["hi"]) for b in data["bands"]),
        )
