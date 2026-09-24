"""Runtime: values, the virtual machine and coroutines.

The interpreter is fully iterative (explicit frame stack), so guest
recursion depth is not bounded by the Python call stack. Execution is
bounded by a per-resume step budget and a machine-wide heap quota.
"""

from collections import namedtuple

# Extra steps granted to run `finally` cleanup after a fault / close.
GRACE_STEPS = 2000


class VmError(Exception):
    """Base class for VM-level failures (uncatchable by guest code)."""


class StepLimitError(VmError):
    pass


class HeapQuotaError(VmError):
    pass


class StateError(VmError):
    pass


class HostError(VmError):
    pass


class SnapshotError(VmError):
    pass


class GuestUncaught(VmError):
    """A guest `throw` that escaped all handlers."""

    def __init__(self, value):
        super().__init__(f"uncaught guest exception: {value!r}")
        self.value = value


class _Cancel:
    """Internal token used to unwind a coroutine on close()."""

    _instance = None

    def __repr__(self):
        return "<cancel>"


class _Done:
    def __repr__(self):
        return "DONE"


DONE = _Done()


class Env:
    __slots__ = ("cells", "parent", "is_global", "__weakref__")

    def __init__(self, cells, parent, is_global=False):
        self.cells = cells
        self.parent = parent
        self.is_global = is_global


class Closure:
    __slots__ = ("code", "env", "__weakref__")

    def __init__(self, code, env):
        self.code = code
        self.env = env

    def __repr__(self):
        return f"<closure {self.code.name}>"


class GuestIter:
    __slots__ = ("items", "index", "__weakref__")

    def __init__(self, items, index=0):
        self.items = items
        self.index = index


Handler = namedtuple("Handler", ["catch", "finally_addr", "depth", "env"])


class Frame:
    __slots__ = ("code", "ip", "stack", "env", "handlers", "pending", "__weakref__")

    def __init__(self, code, ip, stack, env, handlers, pending=None):
        self.code = code
        self.ip = ip
        self.stack = stack
        self.env = env
        self.handlers = handlers
        self.pending = pending  # (exc, catchable) while running a finally


# ---------------------------------------------------------------- builtins

def _bn_list(machine, *args):
    machine.alloc()
    return list(args)


def _bn_print(machine, *args):
    machine.output.append(" ".join(str(a) for a in args))
    return None


def _bn_push(machine, lst, value):
    lst.append(value)
    return lst


def _bn_set(machine, lst, index, value):
    lst[index] = value
    return value


def _bn_sub(machine, a, *rest):
    if not rest:
        return -a
    for x in rest:
        a -= x
    return a


def _bn_mul(machine, *args):
    result = 1
    for x in args:
        result *= x
    return result


BUILTINS = {
    "+": lambda m, *a: sum(a),
    "-": _bn_sub,
    "*": _bn_mul,
    "==": lambda m, a, b: a == b,
    "!=": lambda m, a, b: a != b,
    "<": lambda m, a, b: a < b,
    "<=": lambda m, a, b: a <= b,
    ">": lambda m, a, b: a > b,
    ">=": lambda m, a, b: a >= b,
    "not": lambda m, x: not x,
    "len": lambda m, x: len(x),
    "list": _bn_list,
    "list-get": lambda m, x, i: x[i],
    "list-set!": _bn_set,
    "push": _bn_push,
    "print": _bn_print,
}


# ---------------------------------------------------------------- machine

class Machine:
    """Owns the program, globals, heap quota and output buffer."""

    def __init__(self, program, globals=None, heap_quota=1_000_000):
        self.program = program
        self.heap_quota = heap_quota
        self.heap = 0
        self.output = []
        self.globals = dict(BUILTINS)
        if globals:
            self.globals.update(globals)
        self.global_env = Env([], None, is_global=True)

    def alloc(self, n=1):
        self.heap += n
        if self.heap > self.heap_quota:
            raise HeapQuotaError(f"heap quota exceeded ({self.heap_quota})")

    def start(self):
        """Create the main coroutine (not yet running)."""
        co = Coroutine(self)
        co.frames.append(Frame(self.program.main, 0, [], self.global_env, []))
        return co


# ---------------------------------------------------------------- coroutine

class Coroutine:
    """A resumable unit of execution.

    status: new -> running -> suspended -> running ... -> done|closed|error
    """

    __slots__ = (
        "machine", "frames", "status", "yielded", "result", "error",
        "_canceling", "_failing", "__weakref__",
    )

    def __init__(self, machine):
        self.machine = machine
        self.frames = []
        self.status = "new"
        self.yielded = None
        self.result = None
        self.error = None
        self._canceling = False
        self._failing = False

    # -- public API ------------------------------------------------------

    def resume(self, value=None, max_steps=100_000):
        if self.status == "new":
            pass
        elif self.status == "suspended":
            self.frames[-1].stack.append(value)
        else:
            raise StateError(f"cannot resume coroutine in state {self.status!r}")
        self.status = "running"
        try:
            out = self._run(max_steps)
        except VmError as exc:
            self._fail(exc)
            raise
        if out == "yield":
            self.status = "suspended"
            return self.yielded
        if self._canceling:
            self.status = "closed"
        elif not self._failing:
            self.status = "done"
        return DONE

    def close(self):
        """Cancel the coroutine: run pending finally blocks exactly once."""
        if self.status in ("done", "closed", "error"):
            return
        self._canceling = True
        try:
            if self._unwind(_Cancel(), False):
                try:
                    self._run(GRACE_STEPS)
                except VmError:
                    pass
        finally:
            self._canceling = False
        self.frames.clear()
        self.status = "closed"

    def snapshot(self):
        from . import snapshot as _snapshot

        return _snapshot.to_data(self)

    # -- internals -------------------------------------------------------

    def _fail(self, exc):
        """Uncatchable fault: run finally cleanup, then become dead."""
        self._failing = True
        try:
            if self._unwind(exc, False):
                try:
                    self._run(GRACE_STEPS)
                except VmError:
                    pass
        finally:
            self._failing = False
        self.frames.clear()
        self.status = "error"
        self.error = exc

    def _unwind(self, exc, catchable):
        """Unwind to the nearest handler; run it. Returns True if handled."""
        while self.frames:
            frame = self.frames[-1]
            while frame.handlers:
                handler = frame.handlers.pop()
                if catchable and handler.catch is not None:
                    frame.env = handler.env
                    del frame.stack[handler.depth:]
                    frame.pending = None
                    frame.stack.append(exc)
                    frame.ip = handler.catch
                    return True
                if handler.finally_addr is not None:
                    frame.env = handler.env
                    del frame.stack[handler.depth:]
                    frame.pending = (exc, catchable)
                    frame.ip = handler.finally_addr
                    return True
                # catch-only handler that does not apply: keep unwinding
            self.frames.pop()
        return False

    def _call(self, frame, nargs):
        stack = frame.stack
        if nargs:
            args = stack[-nargs:]
            del stack[-nargs:]
        else:
            args = []
        callee = stack.pop()
        machine = self.machine
        if isinstance(callee, Closure):
            code = callee.code
            if len(args) != code.nparams:
                raise HostError(
                    f"{code.name}: expected {code.nparams} args, got {len(args)}"
                )
            machine.alloc()
            env = Env(list(args) + [None] * (code.nslots - code.nparams), callee.env)
            self.frames.append(Frame(code, 0, [], env, []))
        elif callable(callee):
            try:
                result = callee(machine, *args)
            except VmError:
                raise
            except Exception as exc:
                raise HostError(f"host error in builtin: {exc}") from exc
            stack.append(result)
        else:
            raise HostError(f"not callable: {callee!r}")

    def _run(self, max_steps):
        frames = self.frames
        machine = self.machine
        steps = 0
        while frames:
            frame = frames[-1]
            instr = frame.code.instrs[frame.ip]
            frame.ip += 1
            steps += 1
            if steps > max_steps:
                raise StepLimitError(f"step limit exceeded ({max_steps})")
            op = instr[0]
            stack = frame.stack
            if op == "CONST":
                stack.append(instr[1])
            elif op == "LOAD":
                env = frame.env
                for _ in range(instr[1]):
                    env = env.parent
                stack.append(env.cells[instr[2]])
            elif op == "STORE":
                env = frame.env
                for _ in range(instr[1]):
                    env = env.parent
                env.cells[instr[2]] = stack[-1]
            elif op == "DEF":
                frame.env.cells[instr[1]] = stack.pop()
            elif op == "LOAD_GLOBAL":
                name = instr[1]
                try:
                    stack.append(machine.globals[name])
                except KeyError:
                    raise HostError(f"undefined global: {name}") from None
            elif op == "STORE_GLOBAL":
                machine.globals[instr[1]] = stack[-1]
            elif op == "POP":
                stack.pop()
            elif op == "DUP":
                stack.append(stack[-1])
            elif op == "JUMP":
                frame.ip = instr[1]
            elif op == "JUMP_IF_FALSE":
                if not stack.pop():
                    frame.ip = instr[1]
            elif op == "JUMP_IF_TRUE":
                if stack.pop():
                    frame.ip = instr[1]
            elif op == "ENTER":
                machine.alloc()
                frame.env = Env([None] * instr[1], frame.env)
            elif op == "EXIT":
                frame.env = frame.env.parent
            elif op == "MAKE_CLOSURE":
                machine.alloc()
                stack.append(Closure(machine.program.codes[instr[1]], frame.env))
            elif op == "CALL":
                self._call(frame, instr[1])
            elif op == "RETURN":
                value = stack.pop() if stack else None
                frames.pop()
                if frames:
                    frames[-1].stack.append(value)
                else:
                    self.result = value
            elif op == "ITER":
                machine.alloc()
                stack.append(GuestIter(stack.pop(), 0))
            elif op == "FOR_NEXT":
                it = stack[-1]
                if it.index >= len(it.items):
                    stack.pop()
                    frame.ip = instr[2]
                else:
                    item = it.items[it.index]
                    it.index += 1
                    machine.alloc()
                    frame.env = Env([item] + [None] * (instr[1] - 1), frame.env)
            elif op == "YIELD":
                if self._canceling or self._failing:
                    raise StateError("cannot yield while unwinding")
                self.yielded = stack.pop()
                return "yield"
            elif op == "TRY_PUSH":
                frame.handlers.append(Handler(instr[1], instr[2], len(stack), frame.env))
            elif op == "TRY_POP":
                frame.handlers.pop()
            elif op == "END_TRY":
                if frame.pending is not None:
                    exc, catchable = frame.pending
                    frame.pending = None
                    if not self._unwind(exc, catchable):
                        if catchable:
                            raise GuestUncaught(exc)
                        if isinstance(exc, VmError):
                            raise exc
                        # _Cancel with nothing left: loop exits below
            elif op == "THROW":
                value = stack.pop()
                if not self._unwind(value, True):
                    raise GuestUncaught(value)
            else:
                raise HostError(f"bad opcode: {op}")
        return "empty"
