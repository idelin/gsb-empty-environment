"""Single-step semantics for simple statements over mutable maps.

Both the abstract interpreter and the concrete executor use this one
implementation, so the two engines can never disagree about what a
statement does.  The function mutates the given maps in place and returns
the list of errors raised by this step (execution continues after errors,
mirroring a concrete run that keeps going so further errors stay visible).
"""
from __future__ import annotations

from .errors import ErrorKind
from .model import (EXCLUSIVE, READ, SHARED, WRITE, Assign, Borrow, Close,
                    EndBorrow, Move, Open, Use)
from .state import RState


def _drop_handle(var, borrows, borrow_of):
    """Forget a borrow handle (overwrite / end), restoring borrow counts."""
    old = borrow_of.pop(var, None)
    if old is not None:
        rid, mode = old
        shared, exclusive = borrows[rid]
        borrows[rid] = (shared - (mode == SHARED),
                        exclusive - (mode == EXCLUSIVE))


def _do_borrow(stmt, env, rstate, borrows, borrow_of, err):
    src = stmt.src
    if src in borrow_of:
        rid, mode = borrow_of[src]
        if mode == SHARED and stmt.mode == EXCLUSIVE:
            err(ErrorKind.BORROW_CONFLICT,
                f"exclusive re-borrow from shared handle '{src}'")
            return
    elif src in env:
        rid = env[src]
    else:
        err(ErrorKind.UNBOUND_VAR, f"unbound variable '{src}'")
        return
    state = rstate[rid]
    if state is RState.CLOSED:
        err(ErrorKind.USE_AFTER_CLOSE, f"borrow of closed resource via '{src}'")
        return
    if state is RState.MOVED:
        err(ErrorKind.USE_AFTER_MOVE, f"borrow of moved resource via '{src}'")
        return
    shared, exclusive = borrows[rid]
    if stmt.mode == EXCLUSIVE and (shared or exclusive):
        err(ErrorKind.BORROW_CONFLICT, "exclusive borrow while borrowed")
        return
    if stmt.mode == SHARED and exclusive:
        err(ErrorKind.BORROW_CONFLICT, "shared borrow while exclusively borrowed")
        return
    _drop_handle(stmt.dst, borrows, borrow_of)
    borrow_of[stmt.dst] = (rid, stmt.mode)
    borrows[rid] = (shared + (stmt.mode == SHARED),
                    exclusive + (stmt.mode == EXCLUSIVE))


def step_simple_maps(stmt, env, rstate, borrows, borrow_of):
    """Execute one simple statement; return [(ErrorKind, detail), ...]."""
    errs = []

    def err(kind, detail):
        errs.append((kind, detail))

    if isinstance(stmt, Open):
        rid = (max(rstate) + 1) if rstate else 0
        _drop_handle(stmt.var, borrows, borrow_of)
        env[stmt.var] = rid
        rstate[rid] = RState.OPEN
        borrows[rid] = (0, 0)
    elif isinstance(stmt, Assign):
        if stmt.src in borrow_of:
            err(ErrorKind.INVALID_OPERATION,
                f"cannot alias borrow handle '{stmt.src}'")
        elif stmt.src not in env:
            err(ErrorKind.UNBOUND_VAR, f"unbound variable '{stmt.src}'")
        else:
            _drop_handle(stmt.dst, borrows, borrow_of)
            env[stmt.dst] = env[stmt.src]
    elif isinstance(stmt, Borrow):
        _do_borrow(stmt, env, rstate, borrows, borrow_of, err)
    elif isinstance(stmt, EndBorrow):
        if stmt.var not in borrow_of:
            err(ErrorKind.INVALID_OPERATION,
                f"'{stmt.var}' is not a live borrow handle")
        else:
            _drop_handle(stmt.var, borrows, borrow_of)
    elif isinstance(stmt, Close):
        if stmt.var in borrow_of:
            err(ErrorKind.INVALID_OPERATION,
                "cannot close through a borrow handle")
        else:
            rid = env.get(stmt.var)
            if rid is None:
                err(ErrorKind.UNBOUND_VAR, f"unbound variable '{stmt.var}'")
            else:
                state = rstate[rid]
                if state is RState.MOVED:
                    err(ErrorKind.USE_AFTER_MOVE,
                        f"close of moved resource via '{stmt.var}'")
                elif state is RState.CLOSED:
                    err(ErrorKind.DOUBLE_CLOSE,
                        f"resource already closed (via '{stmt.var}')")
                else:
                    if borrows[rid] != (0, 0):
                        err(ErrorKind.CLOSE_WHILE_BORROWED,
                            f"resource closed via '{stmt.var}' while borrowed")
                    rstate[rid] = RState.CLOSED
    elif isinstance(stmt, Use):
        via = None
        if stmt.var in borrow_of:
            rid, via = borrow_of[stmt.var]
        else:
            rid = env.get(stmt.var)
            if rid is None:
                err(ErrorKind.UNBOUND_VAR, f"unbound variable '{stmt.var}'")
                return errs
        state = rstate[rid]
        if state is RState.CLOSED:
            err(ErrorKind.USE_AFTER_CLOSE,
                f"{stmt.access} of closed resource via '{stmt.var}'")
        elif state is RState.MOVED:
            err(ErrorKind.USE_AFTER_MOVE,
                f"{stmt.access} of moved resource via '{stmt.var}'")
        else:
            shared, exclusive = borrows[rid]
            if via == SHARED and stmt.access == WRITE:
                err(ErrorKind.BORROW_CONFLICT, "write through shared borrow")
            elif via is None:
                if stmt.access == WRITE and (shared or exclusive):
                    err(ErrorKind.BORROW_CONFLICT, "write while borrowed")
                elif stmt.access == READ and exclusive:
                    err(ErrorKind.BORROW_CONFLICT,
                        "read while exclusively borrowed")
    elif isinstance(stmt, Move):
        if stmt.var in borrow_of:
            err(ErrorKind.INVALID_OPERATION, "cannot move a borrow handle")
        else:
            rid = env.get(stmt.var)
            if rid is None:
                err(ErrorKind.UNBOUND_VAR, f"unbound variable '{stmt.var}'")
            else:
                state = rstate[rid]
                if state is RState.MOVED:
                    err(ErrorKind.USE_AFTER_MOVE,
                        f"resource already moved (via '{stmt.var}')")
                elif state is RState.CLOSED:
                    err(ErrorKind.USE_AFTER_CLOSE,
                        f"move of closed resource via '{stmt.var}'")
                else:
                    if borrows[rid] != (0, 0):
                        err(ErrorKind.BORROW_CONFLICT,
                            f"resource moved via '{stmt.var}' while borrowed")
                    rstate[rid] = RState.MOVED
    else:
        raise TypeError(f"not a simple statement: {stmt!r}")
    return errs
