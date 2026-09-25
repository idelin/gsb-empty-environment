from .lexer import tokenize
from .ast_nodes import (
    Program, Import, ConstDecl, FnDecl, IfStmt, WhileStmt, ReturnStmt,
    PrintStmt, ExprStmt, Num, Str, Bool, Nil, Var, Assign, Binary, Unary,
    Call,
)
from .errors import LangError


class Parser:
    def __init__(self, src):
        self.toks = tokenize(src)
        self.pos = 0

    # ---- token helpers ----

    def peek(self):
        return self.toks[self.pos]

    def advance(self):
        tok = self.toks[self.pos]
        self.pos += 1
        return tok

    def check(self, kind, value=None):
        tok = self.toks[self.pos]
        return tok.kind == kind and (value is None or tok.value == value)

    def expect(self, kind, value=None):
        tok = self.peek()
        if not self.check(kind, value):
            want = value if value is not None else kind
            raise LangError(f"expected {want!r}, got {tok.value!r}",
                            tok.start, tok.end)
        return self.advance()

    # ---- entry ----

    def parse(self):
        stmts = []
        while not self.check("eof"):
            stmts.append(self.stmt())
        return Program(stmts)

    # ---- statements ----

    def stmt(self):
        tok = self.peek()
        if tok.kind == "kw":
            if tok.value == "import":
                return self.import_stmt()
            if tok.value == "export":
                self.advance()
                if self.check("kw", "const"):
                    return self.const_decl(exported=True, mutable=False)
                if self.check("kw", "var"):
                    return self.const_decl(exported=True, mutable=True)
                if self.check("kw", "fn"):
                    return self.fn_decl(exported=True)
                raise LangError("expected 'const', 'var' or 'fn' after "
                                "'export'", tok.start, tok.end)
            if tok.value == "const":
                return self.const_decl(exported=False, mutable=False)
            if tok.value == "var":
                return self.const_decl(exported=False, mutable=True)
            if tok.value == "fn":
                return self.fn_decl(exported=False)
            if tok.value == "return":
                return self.return_stmt()
            if tok.value == "if":
                return self.if_stmt()
            if tok.value == "while":
                return self.while_stmt()
            if tok.value == "print":
                return self.print_stmt()
        expr = self.expr()
        semi = self.expect("punct", ";")
        return ExprStmt(expr, tok.start, semi.end)

    def import_stmt(self):
        kw = self.expect("kw", "import")
        self.expect("punct", "{")
        names = [self.expect("name").value]
        while self.check("punct", ","):
            self.advance()
            names.append(self.expect("name").value)
        self.expect("punct", "}")
        self.expect("kw", "from")
        module = self.expect("str").value
        semi = self.expect("punct", ";")
        return Import(names, module, kw.start, semi.end)

    def const_decl(self, exported, mutable):
        kw = self.advance()  # 'const' or 'var'
        name = self.expect("name").value
        self.expect("punct", "=")
        init = self.expr()
        semi = self.expect("punct", ";")
        return ConstDecl(name, init, exported, mutable, kw.start, semi.end)

    def fn_decl(self, exported):
        kw = self.expect("kw", "fn")
        name = self.expect("name").value
        self.expect("punct", "(")
        params = []
        if not self.check("punct", ")"):
            params.append(self.expect("name").value)
            while self.check("punct", ","):
                self.advance()
                params.append(self.expect("name").value)
        self.expect("punct", ")")
        body, end = self.block()
        return FnDecl(name, params, body, exported, kw.start, end)

    def block(self):
        self.expect("punct", "{")
        stmts = []
        while not self.check("punct", "}"):
            stmts.append(self.stmt())
        end = self.expect("punct", "}").end
        return stmts, end

    def return_stmt(self):
        kw = self.expect("kw", "return")
        if self.check("punct", ";"):
            semi = self.advance()
            return ReturnStmt(None, kw.start, semi.end)
        value = self.expr()
        semi = self.expect("punct", ";")
        return ReturnStmt(value, kw.start, semi.end)

    def if_stmt(self):
        kw = self.expect("kw", "if")
        cond = self.expr()
        then, end = self.block()
        other = []
        if self.check("kw", "else"):
            self.advance()
            other, end = self.block()
        return IfStmt(cond, then, other, kw.start, end)

    def while_stmt(self):
        kw = self.expect("kw", "while")
        cond = self.expr()
        body, end = self.block()
        return WhileStmt(cond, body, kw.start, end)

    def print_stmt(self):
        kw = self.expect("kw", "print")
        self.expect("punct", "(")
        args = []
        if not self.check("punct", ")"):
            args.append(self.expr())
            while self.check("punct", ","):
                self.advance()
                args.append(self.expr())
        self.expect("punct", ")")
        semi = self.expect("punct", ";")
        return PrintStmt(args, kw.start, semi.end)

    # ---- expressions ----

    def expr(self):
        first = self.or_expr()
        if self.check("punct", "="):
            self.advance()
            if not isinstance(first, Var):
                raise LangError("invalid assignment target",
                                first.start, first.end)
            value = self.expr()
            return Assign(first.name, value, first.start, value.end)
        return first

    def or_expr(self):
        left = self.and_expr()
        while self.check("kw", "or"):
            self.advance()
            right = self.and_expr()
            left = Binary("or", left, right, left.start, right.end)
        return left

    def and_expr(self):
        left = self.equality()
        while self.check("kw", "and"):
            self.advance()
            right = self.equality()
            left = Binary("and", left, right, left.start, right.end)
        return left

    def equality(self):
        left = self.comparison()
        while self.check("punct", "==") or self.check("punct", "!="):
            op = self.advance()
            right = self.comparison()
            left = Binary(op.value, left, right, left.start, right.end)
        return left

    def comparison(self):
        left = self.additive()
        while (self.check("punct", "<") or self.check("punct", "<=")
               or self.check("punct", ">") or self.check("punct", ">=")):
            op = self.advance()
            right = self.additive()
            left = Binary(op.value, left, right, left.start, right.end)
        return left

    def additive(self):
        left = self.multiplicative()
        while self.check("punct", "+") or self.check("punct", "-"):
            op = self.advance()
            right = self.multiplicative()
            left = Binary(op.value, left, right, left.start, right.end)
        return left

    def multiplicative(self):
        left = self.unary()
        while (self.check("punct", "*") or self.check("punct", "/")
               or self.check("punct", "%")):
            op = self.advance()
            right = self.unary()
            left = Binary(op.value, left, right, left.start, right.end)
        return left

    def unary(self):
        if self.check("kw", "not") or self.check("punct", "-"):
            op = self.advance()
            operand = self.unary()
            return Unary(op.value, operand, op.start, operand.end)
        return self.call()

    def call(self):
        expr = self.primary()
        while self.check("punct", "("):
            self.advance()
            args = []
            if not self.check("punct", ")"):
                args.append(self.expr())
                while self.check("punct", ","):
                    self.advance()
                    args.append(self.expr())
            close = self.expect("punct", ")")
            expr = Call(expr, args, expr.start, close.end)
        return expr

    def primary(self):
        tok = self.advance()
        if tok.kind == "num":
            value = float(tok.value) if "." in tok.value else int(tok.value)
            return Num(value, tok.start, tok.end)
        if tok.kind == "str":
            return Str(tok.value, tok.start, tok.end)
        if tok.kind == "name":
            return Var(tok.value, tok.start, tok.end)
        if tok.kind == "kw":
            if tok.value == "true":
                return Bool(True, tok.start, tok.end)
            if tok.value == "false":
                return Bool(False, tok.start, tok.end)
            if tok.value == "nil":
                return Nil(tok.start, tok.end)
        if tok.kind == "punct" and tok.value == "(":
            expr = self.expr()
            self.expect("punct", ")")
            return expr
        raise LangError(f"unexpected {tok.value!r}", tok.start, tok.end)


def parse(src):
    return Parser(src).parse()
