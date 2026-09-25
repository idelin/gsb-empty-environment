"""Whitelisted pure builtin functions.

Every builtin draws from the shared budget in proportion to the size of
its inputs and checks limits *before* allocating results, so exponential
or huge constructions fail before memory is committed.
"""

import math
import re

from .budget import MAX_INT
from .errors import ExprError

_INT_RE = re.compile(r"[+-]?[0-9]+\Z")


def _arity(name, args, count, ctx):
    if len(args) != count:
        raise ExprError("function '%s' expects %d argument(s), got %d"
                        % (name, count, len(args)), ctx.start, ctx.end)


def _type_err(name, ctx):
    raise ExprError("function '%s' got invalid argument types" % name,
                    ctx.start, ctx.end)


def _checked_number(value, ctx):
    if isinstance(value, bool):
        return value
    if isinstance(value, int):
        if value > MAX_INT or value < -MAX_INT:
            raise ExprError("number out of range", ctx.start, ctx.end)
        return value
    if not math.isfinite(value):
        raise ExprError("number out of range", ctx.start, ctx.end)
    return value


def _scalar_to_string(value):
    if value is None:
        return "null"
    if value is True:
        return "true"
    if value is False:
        return "false"
    if isinstance(value, str):
        return value
    if isinstance(value, float):
        return repr(value)
    return str(value)


def _to_string(value, ctx):
    if not isinstance(value, list):
        return _scalar_to_string(value)
    parts = ["["]
    stack = [(value, 0)]
    while stack:
        ctx.budget.consume(1, "converting to string")
        lst, index = stack[-1]
        if index < len(lst):
            if index > 0:
                parts.append(", ")
            stack[-1] = (lst, index + 1)
            item = lst[index]
            if isinstance(item, list):
                parts.append("[")
                stack.append((item, 0))
            else:
                parts.append(_scalar_to_string(item))
        else:
            parts.append("]")
            stack.pop()
    result = "".join(parts)
    if len(result) > ctx.limits.max_string:
        raise ExprError("string too long", ctx.start, ctx.end)
    return result


def _fn_len(args, ctx):
    _arity("len", args, 1, ctx)
    if isinstance(args[0], (str, list)):
        return len(args[0])
    raise ExprError("function 'len' expects a string or list",
                    ctx.start, ctx.end)


def _fn_abs(args, ctx):
    _arity("abs", args, 1, ctx)
    if isinstance(args[0], (int, float)):
        return abs(args[0])
    _type_err("abs", ctx)


def _min_max(name, args, ctx):
    if not args:
        raise ExprError("function '%s' expects at least 1 argument" % name,
                        ctx.start, ctx.end)
    first = args[0]
    if isinstance(first, (int, float)):
        if not all(isinstance(a, (int, float)) for a in args):
            _type_err(name, ctx)
    elif isinstance(first, str):
        if not all(isinstance(a, str) for a in args):
            _type_err(name, ctx)
        ctx.budget.consume(sum(len(a) for a in args), name)
    else:
        _type_err(name, ctx)
    ctx.budget.consume(len(args), name)
    return min(args) if name == "min" else max(args)


def _fn_sum(args, ctx):
    _arity("sum", args, 1, ctx)
    lst = args[0]
    if not isinstance(lst, list):
        raise ExprError("function 'sum' expects a list", ctx.start, ctx.end)
    ctx.budget.consume(len(lst), "sum")
    total = 0
    for item in lst:
        if not isinstance(item, (int, float)):
            raise ExprError("function 'sum' expects numbers",
                            ctx.start, ctx.end)
        total = total + item
        _checked_number(total, ctx)
    return total


def _fn_str(args, ctx):
    _arity("str", args, 1, ctx)
    result = _to_string(args[0], ctx)
    ctx.budget.consume(len(result), "converting to string")
    return result


def _fn_int(args, ctx):
    _arity("int", args, 1, ctx)
    value = args[0]
    if isinstance(value, bool):
        return int(value)
    if isinstance(value, int):
        return value
    if isinstance(value, float):
        if not math.isfinite(value):
            raise ExprError("number out of range", ctx.start, ctx.end)
        result = int(value)
        if abs(result) > MAX_INT:
            raise ExprError("number out of range", ctx.start, ctx.end)
        return result
    if isinstance(value, str):
        text = value.strip()
        if not _INT_RE.match(text):
            raise ExprError("cannot convert string to int",
                            ctx.start, ctx.end)
        if len(text.lstrip("+-")) > ctx.limits.max_number_digits:
            raise ExprError("number out of range", ctx.start, ctx.end)
        result = int(text)
        if abs(result) > MAX_INT:
            raise ExprError("number out of range", ctx.start, ctx.end)
        return result
    _type_err("int", ctx)


def _fn_float(args, ctx):
    _arity("float", args, 1, ctx)
    value = args[0]
    if isinstance(value, (int, float)):
        result = float(value)
    elif isinstance(value, str):
        try:
            result = float(value.strip())
        except ValueError:
            raise ExprError("cannot convert string to float",
                            ctx.start, ctx.end) from None
    else:
        _type_err("float", ctx)
    if not math.isfinite(result):
        raise ExprError("number out of range", ctx.start, ctx.end)
    return result


def _fn_bool(args, ctx):
    _arity("bool", args, 1, ctx)
    return bool(args[0])


def _str_method(name, args, ctx, method):
    _arity(name, args, 1, ctx)
    if not isinstance(args[0], str):
        _type_err(name, ctx)
    ctx.budget.consume(len(args[0]), name)
    result = method(args[0])
    if len(result) > ctx.limits.max_string:
        raise ExprError("string too long", ctx.start, ctx.end)
    return result


def _str_pred(name, args, ctx, pred):
    _arity(name, args, 2, ctx)
    text, frag = args
    if not isinstance(text, str) or not isinstance(frag, str):
        _type_err(name, ctx)
    ctx.budget.consume(len(text), name)
    return pred(text, frag)


def _fn_split(args, ctx):
    _arity("split", args, 2, ctx)
    text, sep = args
    if not isinstance(text, str) or not isinstance(sep, str):
        _type_err("split", ctx)
    if sep == "":
        raise ExprError("function 'split' got an empty separator",
                        ctx.start, ctx.end)
    ctx.budget.consume(len(text), "split")
    result = text.split(sep)
    if len(result) > ctx.limits.max_collection:
        raise ExprError("collection too large", ctx.start, ctx.end)
    return result


def _fn_join(args, ctx):
    _arity("join", args, 2, ctx)
    parts, sep = args
    if not isinstance(parts, list) or not isinstance(sep, str):
        _type_err("join", ctx)
    if not all(isinstance(p, str) for p in parts):
        raise ExprError("function 'join' expects a list of strings",
                        ctx.start, ctx.end)
    total = sum(len(p) for p in parts) + len(sep) * max(0, len(parts) - 1)
    if total > ctx.limits.max_string:
        raise ExprError("string too long", ctx.start, ctx.end)
    ctx.budget.consume(total, "join")
    return sep.join(parts)


def _fn_concat(args, ctx):
    _arity("concat", args, 2, ctx)
    left, right = args
    if isinstance(left, str) and isinstance(right, str):
        total = len(left) + len(right)
        if total > ctx.limits.max_string:
            raise ExprError("string too long", ctx.start, ctx.end)
        ctx.budget.consume(total, "string concatenation")
        return left + right
    if isinstance(left, list) and isinstance(right, list):
        total = len(left) + len(right)
        if total > ctx.limits.max_collection:
            raise ExprError("collection too large", ctx.start, ctx.end)
        ctx.budget.consume(total, "list concatenation")
        return left + right
    _type_err("concat", ctx)


def _fn_repeat(args, ctx):
    _arity("repeat", args, 2, ctx)
    value, count = args
    if isinstance(count, bool) or not isinstance(count, int):
        _type_err("repeat", ctx)
    if isinstance(value, str):
        limit, what = ctx.limits.max_string, "string too long"
        empty = ""
    elif isinstance(value, list):
        limit, what = ctx.limits.max_collection, "collection too large"
        empty = []
    else:
        _type_err("repeat", ctx)
    if count <= 0:
        return empty
    size = len(value) * count
    # Limit and budget are both checked before anything is allocated.
    if size > limit:
        raise ExprError(what, ctx.start, ctx.end)
    ctx.budget.consume(size, "repeat")
    return value * count


def _fn_range(args, ctx):
    if not 1 <= len(args) <= 3:
        raise ExprError("function 'range' expects 1 to 3 arguments, got %d"
                        % len(args), ctx.start, ctx.end)
    if any(isinstance(a, bool) or not isinstance(a, int) for a in args):
        _type_err("range", ctx)
    if len(args) == 1:
        start, stop, step = 0, args[0], 1
    elif len(args) == 2:
        start, stop, step = args[0], args[1], 1
    else:
        start, stop, step = args
    if step == 0:
        raise ExprError("function 'range' got a zero step",
                        ctx.start, ctx.end)
    count = len(range(start, stop, step))
    if count > ctx.limits.max_collection:
        raise ExprError("collection too large", ctx.start, ctx.end)
    ctx.budget.consume(count, "range")
    return list(range(start, stop, step))


def _fn_reverse(args, ctx):
    _arity("reverse", args, 1, ctx)
    value = args[0]
    if isinstance(value, str):
        ctx.budget.consume(len(value), "reverse")
        return value[::-1]
    if isinstance(value, list):
        ctx.budget.consume(len(value), "reverse")
        return value[::-1]
    _type_err("reverse", ctx)


def _fn_sort(args, ctx):
    _arity("sort", args, 1, ctx)
    lst = args[0]
    if not isinstance(lst, list):
        _type_err("sort", ctx)
    ctx.budget.consume(len(lst), "sort")
    if all(isinstance(i, (int, float)) for i in lst):
        return sorted(lst)
    if all(isinstance(i, str) for i in lst):
        return sorted(lst)
    raise ExprError("function 'sort' expects a list of numbers or strings",
                    ctx.start, ctx.end)


def _fn_get(args, ctx):
    _arity("get", args, 2, ctx)
    value, index = args
    if isinstance(index, bool) or not isinstance(index, int):
        _type_err("get", ctx)
    if not isinstance(value, (str, list)):
        _type_err("get", ctx)
    resolved = index if index >= 0 else len(value) + index
    if resolved < 0 or resolved >= len(value):
        raise ExprError("index out of range", ctx.start, ctx.end)
    return value[resolved]


def _fn_slice(args, ctx):
    _arity("slice", args, 3, ctx)
    value, start, stop = args
    if not isinstance(value, (str, list)):
        _type_err("slice", ctx)
    if any(isinstance(a, bool) or not isinstance(a, int)
           for a in (start, stop)):
        _type_err("slice", ctx)
    result = value[start:stop]
    ctx.budget.consume(len(result), "slice")
    return result


BUILTINS = {
    "len": _fn_len,
    "abs": _fn_abs,
    "min": lambda args, ctx: _min_max("min", args, ctx),
    "max": lambda args, ctx: _min_max("max", args, ctx),
    "sum": _fn_sum,
    "str": _fn_str,
    "int": _fn_int,
    "float": _fn_float,
    "bool": _fn_bool,
    "upper": lambda a, c: _str_method("upper", a, c, str.upper),
    "lower": lambda a, c: _str_method("lower", a, c, str.lower),
    "strip": lambda a, c: _str_method("strip", a, c, str.strip),
    "startswith": lambda a, c: _str_pred("startswith", a, c, str.startswith),
    "endswith": lambda a, c: _str_pred("endswith", a, c, str.endswith),
    "contains": lambda a, c: _str_pred("contains", a, c,
                                       lambda s, f: f in s),
    "split": _fn_split,
    "join": _fn_join,
    "concat": _fn_concat,
    "repeat": _fn_repeat,
    "range": _fn_range,
    "reverse": _fn_reverse,
    "sort": _fn_sort,
    "get": _fn_get,
    "slice": _fn_slice,
}
