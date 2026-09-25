"""Gap detection. Only NaN counts as missing; a true zero is an observation."""

import math
from dataclasses import dataclass


@dataclass(frozen=True)
class Gap:
    start: int
    length: int

    @property
    def end(self) -> int:
        return self.start + self.length


def find_gaps(values) -> list:
    gaps = []
    index = 0
    total = len(values)
    while index < total:
        if math.isnan(values[index]):
            stop = index
            while stop < total and math.isnan(values[stop]):
                stop += 1
            gaps.append(Gap(index, stop - index))
            index = stop
        else:
            index += 1
    return gaps
