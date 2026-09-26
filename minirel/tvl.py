"""Three-valued logic: TRUE / FALSE / UNKNOWN (UNKNOWN is None)."""

UNKNOWN = None


def tvl_and(a, b):
    if a is False or b is False:
        return False
    if a is None or b is None:
        return None
    return True


def tvl_or(a, b):
    if a is True or b is True:
        return True
    if a is None or b is None:
        return None
    return False


def tvl_not(a):
    if a is None:
        return None
    return not a


def eq3(a, b):
    """Regular SQL equality: unknown if either side is null."""
    if a is None or b is None:
        return None
    return a == b


def eq_nullsafe(a, b):
    """Null-safe equality (IS NOT DISTINCT FROM)."""
    if a is None or b is None:
        return a is None and b is None
    return a == b


def row_eq(xs, ys):
    """Row-value comparison: three-valued AND of per-component equality.

    A single definitely-unequal component makes the whole row FALSE even if
    other components are UNKNOWN.
    """
    result = True
    for x, y in zip(xs, ys):
        result = tvl_and(result, eq3(x, y))
        if result is False:
            break
    return result
