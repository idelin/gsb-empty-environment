"""Compiler: AST -> bytecode.

Lexical scoping is resolved at compile time to (depth, slot) addresses.
At runtime the environment chain mirrors the compile-time scope chain:
function calls, ENTER (block scope), FOR_NEXT (fresh per-iteration
scope) and catch bindings each push one environment.
"""

import hashlib
import json

from .parser import Sym, Num, Str, List as ListNode, parse

BC_VERSION = 1

_LITERAL_SYMS = {"true": True, "false": False, "nil": None}


class CompileError(Exception):
    pass


class Code:
    __slots__ = ("name", "instrs", "nparams", "nslots")

    def __init__(self, name, nparams=0):
        self.name = name
        self.instrs = []
        self.nparams = nparams
        self.nslots = 0

    def __repr__(self):
        return f"Code({self.name!r}, {len(self.instrs)} instrs)"


class Program:
    """A compiled program. `digest` identifies version + bytecode exactly."""

    def __init__(self, codes):
        self.codes = list(codes)
        self._digest = self._compute_digest()

    @property
    def main(self):
        return self.codes[0]

    @property
    def digest(self):
        return self._digest

    def canonical(self):
        return {
            "bytecode_version": BC_VERSION,
            "codes": [
                {
                    "name": c.name,
                    "nparams": c.nparams,
                    "nslots": c.nslots,
                    "instrs": [list(i) for i in c.instrs],
                }
                for c in self.codes
            ],
        }

    def _compute_digest(self):
        blob = json.dumps(self.canonical(), sort_keys=True, separators=(",", ":"))
        return hashlib.sha256(blob.encode("utf-8")).hexdigest()


class _Scope:
    __slots__ = ("parent", "names")

    def __init__(self, parent=None):
        self.parent = parent
        self.names = {}

    def define(self, name):
        if name in self.names:
            raise CompileError(f"duplicate binding: {name}")
        self.names[name] = len(self.names)
        return self.names[name]

    def resolve(self, name):
        scope = self
        depth = 0
        while scope is not None:
            if name in scope.names:
                return depth, scope.names[name]
            scope = scope.parent
            depth += 1
        return None


class _Compiler:
    def __init__(self):
        self.codes = []

    # -- helpers ---------------------------------------------------------

    @staticmethod
    def _emit(code, instr):
        code.instrs.append(instr)
        return len(code.instrs) - 1

    def _body(self, code, scope, nodes):
        """Compile a sequence; the value of the last form stays on stack."""
        if not nodes:
            code.instrs.append(("CONST", None))
            return
        # Hoist `def` names so (mutually) recursive definitions resolve.
        for node in nodes:
            if (
                isinstance(node, ListNode)
                and len(node.items) >= 2
                and isinstance(node.items[0], Sym)
                and node.items[0].name == "def"
                and isinstance(node.items[1], Sym)
            ):
                scope.define(node.items[1].name)
        for node in nodes[:-1]:
            self._expr(code, scope, node)
            code.instrs.append(("POP",))
        self._expr(code, scope, nodes[-1])

    # -- entry point -----------------------------------------------------

    def compile(self, nodes):
        main = Code("<main>", 0)
        self.codes.append(main)
        scope = _Scope(None)
        main.instrs.append(("ENTER", 0))  # patched below
        self._body(main, scope, nodes)
        main.instrs.append(("RETURN",))
        main.instrs[0] = ("ENTER", len(scope.names))
        main.nslots = len(scope.names)
        return Program(self.codes)

    # -- expressions -----------------------------------------------------

    def _expr(self, code, scope, node):
        instrs = code.instrs
        if isinstance(node, Num):
            instrs.append(("CONST", node.value))
            return
        if isinstance(node, Str):
            instrs.append(("CONST", node.value))
            return
        if isinstance(node, Sym):
            name = node.name
            if name in _LITERAL_SYMS:
                instrs.append(("CONST", _LITERAL_SYMS[name]))
                return
            found = scope.resolve(name)
            if found is not None:
                instrs.append(("LOAD", found[0], found[1]))
            else:
                instrs.append(("LOAD_GLOBAL", name))
            return
        # list form
        items = node.items
        if not items:
            raise CompileError("empty form ()")
        head = items[0]
        if isinstance(head, Sym):
            special = self._SPECIALS.get(head.name)
            if special is not None:
                special(self, code, scope, node)
                return
        self._expr(code, scope, head)
        for arg in items[1:]:
            self._expr(code, scope, arg)
        instrs.append(("CALL", len(items) - 1))

    def _function(self, name, params_node, body, defscope):
        if not isinstance(params_node, ListNode):
            raise CompileError("fn: parameter list must be a list")
        fscope = _Scope(defscope)
        for p in params_node.items:
            if not isinstance(p, Sym):
                raise CompileError("fn: parameter must be a symbol")
            fscope.define(p.name)
        fcode = Code(name, len(params_node.items))
        self.codes.append(fcode)
        idx = len(self.codes) - 1
        self._body(fcode, fscope, body)
        fcode.instrs.append(("RETURN",))
        fcode.nslots = len(fscope.names)
        return idx

    # -- special forms ---------------------------------------------------

    def _do(self, code, scope, node):
        self._body(code, scope, node.items[1:])

    def _let(self, code, scope, node):
        if len(node.items) != 3 or not isinstance(node.items[1], Sym):
            raise CompileError("let: expected (let name init)")
        self._expr(code, scope, node.items[2])
        slot = scope.define(node.items[1].name)
        code.instrs.append(("DEF", slot))
        code.instrs.append(("CONST", None))

    def _set(self, code, scope, node):
        if len(node.items) != 3 or not isinstance(node.items[1], Sym):
            raise CompileError("set: expected (set name expr)")
        name = node.items[1].name
        self._expr(code, scope, node.items[2])
        found = scope.resolve(name)
        if found is not None:
            code.instrs.append(("STORE", found[0], found[1]))
        else:
            code.instrs.append(("STORE_GLOBAL", name))

    def _fn(self, code, scope, node):
        if len(node.items) < 3:
            raise CompileError("fn: expected (fn (params) body...)")
        idx = self._function("<fn>", node.items[1], node.items[2:], scope)
        code.instrs.append(("MAKE_CLOSURE", idx))

    def _def(self, code, scope, node):
        if len(node.items) < 4 or not isinstance(node.items[1], Sym):
            raise CompileError("def: expected (def name (params) body...)")
        name = node.items[1].name
        slot = scope.names.get(name)
        if slot is None:
            slot = scope.define(name)
        idx = self._function(name, node.items[2], node.items[3:], scope)
        code.instrs.append(("MAKE_CLOSURE", idx))
        code.instrs.append(("DEF", slot))
        code.instrs.append(("CONST", None))

    def _if(self, code, scope, node):
        if len(node.items) not in (3, 4):
            raise CompileError("if: expected (if cond then [else])")
        instrs = code.instrs
        self._expr(code, scope, node.items[1])
        jf = self._emit(code, ("JUMP_IF_FALSE", 0))
        self._expr(code, scope, node.items[2])
        jend = self._emit(code, ("JUMP", 0))
        instrs[jf] = ("JUMP_IF_FALSE", len(instrs))
        if len(node.items) == 4:
            self._expr(code, scope, node.items[3])
        else:
            instrs.append(("CONST", None))
        instrs[jend] = ("JUMP", len(instrs))

    def _while(self, code, scope, node):
        if len(node.items) < 2:
            raise CompileError("while: expected (while cond body...)")
        instrs = code.instrs
        start = len(instrs)
        self._expr(code, scope, node.items[1])
        jf = self._emit(code, ("JUMP_IF_FALSE", 0))
        wscope = _Scope(scope)
        enter = self._emit(code, ("ENTER", 0))
        for item in node.items[2:]:
            self._expr(code, wscope, item)
            instrs.append(("POP",))
        instrs.append(("EXIT",))
        instrs.append(("JUMP", start))
        end = len(instrs)
        instrs[jf] = ("JUMP_IF_FALSE", end)
        instrs[enter] = ("ENTER", len(wscope.names))
        instrs.append(("CONST", None))

    def _each(self, code, scope, node):
        # (each var list-expr body...) -- fresh binding per iteration, so
        # closures captured in the body are independent of each other.
        if len(node.items) < 4 or not isinstance(node.items[1], Sym):
            raise CompileError("each: expected (each var list body...)")
        instrs = code.instrs
        self._expr(code, scope, node.items[2])
        instrs.append(("ITER",))
        loop = len(instrs)
        escope = _Scope(scope)
        escope.define(node.items[1].name)
        fornext = self._emit(code, ("FOR_NEXT", 0, 0))
        for item in node.items[3:]:
            self._expr(code, escope, item)
            instrs.append(("POP",))
        instrs.append(("EXIT",))
        instrs.append(("JUMP", loop))
        end = len(instrs)
        instrs[fornext] = ("FOR_NEXT", len(escope.names), end)
        instrs.append(("CONST", None))

    def _try(self, code, scope, node):
        # (try body... [(catch e h...)] [(finally f...)])
        instrs = code.instrs
        body, catch, finally_ = [], None, None
        for item in node.items[1:]:
            clause = None
            if isinstance(item, ListNode) and item.items and isinstance(item.items[0], Sym):
                clause = item.items[0].name
            if clause == "catch":
                if catch is not None or finally_ is not None:
                    raise CompileError("try: misplaced catch clause")
                catch = item
            elif clause == "finally":
                if finally_ is not None:
                    raise CompileError("try: duplicate finally clause")
                finally_ = item
            else:
                if catch is not None or finally_ is not None:
                    raise CompileError("try: body form after clause")
                body.append(item)
        if catch is None and finally_ is None:
            raise CompileError("try: needs a catch or finally clause")
        push = self._emit(code, ("TRY_PUSH", None, None))
        self._body(code, scope, body)
        instrs.append(("TRY_POP",))
        normal_jump = self._emit(code, ("JUMP", 0))
        catch_addr = None
        if catch is not None:
            if len(catch.items) < 2 or not isinstance(catch.items[1], Sym):
                raise CompileError("catch: expected (catch e body...)")
            catch_addr = len(instrs)
            cscope = _Scope(scope)
            cscope.define(catch.items[1].name)
            enter = self._emit(code, ("ENTER", 0))
            instrs.append(("DEF", 0))
            instrs.append(("CONST", None))
            instrs.append(("POP",))
            self._body(code, cscope, catch.items[2:])
            instrs.append(("EXIT",))
            instrs[enter] = ("ENTER", len(cscope.names))
        finally_addr = None
        if finally_ is not None:
            finally_addr = len(instrs)
            for item in finally_.items[1:]:
                self._expr(code, scope, item)
                instrs.append(("POP",))
            instrs.append(("END_TRY",))
        end = len(instrs)
        instrs[push] = ("TRY_PUSH", catch_addr, finally_addr)
        instrs[normal_jump] = ("JUMP", finally_addr if finally_ is not None else end)

    def _throw(self, code, scope, node):
        if len(node.items) != 2:
            raise CompileError("throw: expected (throw expr)")
        self._expr(code, scope, node.items[1])
        code.instrs.append(("THROW",))

    def _yield(self, code, scope, node):
        if len(node.items) == 1:
            code.instrs.append(("CONST", None))
        elif len(node.items) == 2:
            self._expr(code, scope, node.items[1])
        else:
            raise CompileError("yield: expected (yield [expr])")
        code.instrs.append(("YIELD",))

    def _and(self, code, scope, node):
        args = node.items[1:]
        instrs = code.instrs
        if not args:
            instrs.append(("CONST", True))
            return
        jumps = []
        for arg in args[:-1]:
            self._expr(code, scope, arg)
            instrs.append(("DUP",))
            jumps.append(self._emit(code, ("JUMP_IF_FALSE", 0)))
            instrs.append(("POP",))
        self._expr(code, scope, args[-1])
        end = len(instrs)
        for j in jumps:
            instrs[j] = ("JUMP_IF_FALSE", end)

    def _or(self, code, scope, node):
        args = node.items[1:]
        instrs = code.instrs
        if not args:
            instrs.append(("CONST", False))
            return
        jumps = []
        for arg in args[:-1]:
            self._expr(code, scope, arg)
            instrs.append(("DUP",))
            jumps.append(self._emit(code, ("JUMP_IF_TRUE", 0)))
            instrs.append(("POP",))
        self._expr(code, scope, args[-1])
        end = len(instrs)
        for j in jumps:
            instrs[j] = ("JUMP_IF_TRUE", end)

    _SPECIALS = {}


_Compiler._SPECIALS = {
    "do": _Compiler._do,
    "let": _Compiler._let,
    "set": _Compiler._set,
    "fn": _Compiler._fn,
    "def": _Compiler._def,
    "if": _Compiler._if,
    "while": _Compiler._while,
    "each": _Compiler._each,
    "try": _Compiler._try,
    "throw": _Compiler._throw,
    "yield": _Compiler._yield,
    "and": _Compiler._and,
    "or": _Compiler._or,
}


def compile_program(source):
    """Compile source text into a Program."""
    return _Compiler().compile(parse(source))
