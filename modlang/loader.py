import os

from .ast_nodes import Import
from .errors import LangError
from .interpreter import Env, Interpreter
from .parser import parse


class Module:
    """A loaded module: AST plus its place in the initialization graph."""

    def __init__(self, path, name, program):
        self.path = path
        self.name = name
        self.program = program
        self.state = "new"   # new -> initializing -> initialized
        self.exports = {}
        self.env = Env(None)  # module-local bindings
        self.deps = []        # imported modules (initialization graph)


class Loader:
    """Loads modules, builds the initialization graph, initializes each
    module exactly once (cached by absolute path)."""

    def __init__(self, out=None):
        self.out = out if out is not None else (lambda text: print(text))
        self.cache = {}  # absolute path -> Module
        self.interp = Interpreter(self)

    def run(self, path):
        module = self.load(os.path.abspath(path))
        self.ensure_init(module)
        return module

    def load(self, path):
        path = os.path.abspath(path)
        if path in self.cache:
            return self.cache[path]
        if not os.path.exists(path):
            raise LangError(f"module not found: {path}")
        with open(path, "r", encoding="utf-8") as handle:
            src = handle.read()
        program = parse(src)
        name = os.path.splitext(os.path.basename(path))[0]
        module = Module(path, name, program)
        # Insert into the cache before recursing so import cycles
        # terminate while building the graph.
        self.cache[path] = module
        for stmt in program.stmts:
            if isinstance(stmt, Import):
                dep = self.load(self.resolve(path, stmt.module))
                module.deps.append(dep)
        return module

    def resolve(self, importer_path, spec):
        candidate = os.path.join(os.path.dirname(importer_path), spec)
        if not candidate.endswith(".mod"):
            candidate += ".mod"
        return candidate

    def load_import(self, module, import_stmt):
        return self.load(self.resolve(module.path, import_stmt.module))

    def ensure_init(self, module):
        # Already initializing (cycle) or done: never initialize twice.
        if module.state != "new":
            return
        module.state = "initializing"
        self.interp.init_module(module)
        module.state = "initialized"
