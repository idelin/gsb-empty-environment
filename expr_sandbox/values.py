"""Plain-value validation and normalization.

Only plain data may cross the boundary between the executor and the host:
None, bool, int, float, str and list.  Everything else (host objects,
tuples, dicts, generators, ...) is rejected.  Normalization also returns
fresh exact-type copies so subclasses with hostile behaviour cannot leak
into the executor.  The walk is iterative and cycle-safe.
"""

import math

from .budget import MAX_INT
from .errors import ExprError


def normalize(value, limits, budget, who, start=None, end=None):
    """Return a plain-data copy of ``value`` or raise ExprError.

    ``who`` is a short safe description used in error messages, e.g.
    ``"function 'f'"`` or ``"variable 'x'"``.
    """
    stack = [(value, False, 0)]
    out = []
    active = set()
    while stack:
        item, processed, depth = stack.pop()
        budget.consume(1, "validating values")
        if processed:
            count = len(item)
            children = out[len(out) - count:]
            del out[len(out) - count:]
            active.discard(id(item))
            out.append(children)
            continue
        if item is None or isinstance(item, bool):
            out.append(item)
        elif isinstance(item, int):
            if item > MAX_INT or item < -MAX_INT:
                raise ExprError("%s produced a number out of range" % who,
                                start, end)
            out.append(int(item))
        elif isinstance(item, float):
            if not math.isfinite(item):
                raise ExprError("%s produced a number out of range" % who,
                                start, end)
            out.append(float(item))
        elif isinstance(item, str):
            if len(item) > limits.max_string:
                raise ExprError("%s produced a string too long" % who,
                                start, end)
            out.append(str(item))
        elif isinstance(item, list):
            if len(item) > limits.max_collection:
                raise ExprError("%s produced a collection too large" % who,
                                start, end)
            if depth + 1 > limits.max_depth:
                raise ExprError("%s produced a value nested too deep" % who,
                                start, end)
            if id(item) in active:
                raise ExprError("%s produced a cyclic value" % who,
                                start, end)
            active.add(id(item))
            stack.append((item, True, depth))
            for child in reversed(item):
                stack.append((child, False, depth + 1))
        else:
            raise ExprError(
                "%s produced a forbidden value of type '%s'"
                % (who, type(item).__name__), start, end)
    return out[0]
