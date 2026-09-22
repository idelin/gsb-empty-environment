"""Hand-written recursive-descent parser.

Grammar (informal)::

    program     := statement* EOF
    statement   := letStmt | funcDecl | ifStmt | whileStmt | forStmt
                 | breakStmt | continueStmt | returnStmt | throwStmt
                 | tryStmt | block | exprStmt
    letStmt     := "let" IDENT ("=" expression)? ";"
    funcDecl    := "fn" IDENT "(" (IDENT ("," IDENT)*)? ")" block
    ifStmt      := "if" "(" expression ")" statement ("else" statement)?
    whileStmt   := "while" "(" expression ")" statement
    forStmt     := "for" "(" (letStmt | exprStmt | ";")
                        expression? ";" expression? ")" statement
    breakStmt   := "break" ";"
    continueStmt:= "continue" ";"
    returnStmt  := "return" expression? ";"
    throwStmt   := "throw" expression ";"
    tryStmt     := "try" block ("catch" "(" IDENT ")" block)? ("finally" block)?
                   -- at least one of catch / finally is required
    block       := "{" statement* "}"
    exprStmt    := expression ";"

    expression  := assignment
    assignment  := IDENT "=" assignment | logicOr
    logicOr     := logicAnd ("||" logicAnd)*
    logicAnd    := equality ("&&" equality)*
    equality    := comparison (("==" | "!=") comparison)*
    comparison  := term (("<" | "<=" | ">" | ">=") term)*
    term        := factor (("+" | "-") factor)*
    factor      := unary (("*" | "/" | "%") unary)*
    unary       := ("!" | "-") unary | call
    call        := primary ("(" (expression ("," expression)*)? ")")*
    primary     := NUMBER | STRING | "true" | "false" | "nil"
                 | IDENT | "(" expression ")"
"""
from __future__ import annotations

from .ast_nodes import (
    Assign,
    Binary,
    Block,
    BreakStmt,
    Call,
    ContinueStmt,
    ExprStmt,
    ForStmt,
    FuncDecl,
    IfStmt,
    LetStmt,
    Literal,
    Logical,
    Program,
    ReturnStmt,
    ThrowStmt,
    TryStmt,
    Unary,
    Var,
    WhileStmt,
)
from .lexer import Token


class ParseError(Exception):
    def __init__(self, message: str, start: int, end: int):
        super().__init__(f"{message} (bytes {start}..{end})")
        self.message = message
        self.start = start
        self.end = end


class Parser:
    def __init__(self, tokens: list[Token]):
        self.tokens = tokens
        self.current = 0

    # -- entry point ----------------------------------------------------

    def parse(self) -> Program:
        stmts = []
        while not self._at_end():
            stmts.append(self._statement())
        eof = self._peek()
        start = stmts[0].start if stmts else eof.start
        return Program(start=start, end=eof.end, stmts=stmts)

    # -- token helpers --------------------------------------------------

    def _peek(self) -> Token:
        return self.tokens[self.current]

    def _at_end(self) -> bool:
        return self._peek().kind == "EOF"

    def _advance(self) -> Token:
        tok = self._peek()
        if tok.kind != "EOF":
            self.current += 1
        return tok

    def _check(self, kind: str) -> bool:
        return self._peek().kind == kind

    def _match(self, *kinds: str):
        if self._peek().kind in kinds:
            return self._advance()
        return None

    def _expect(self, kind: str, what: str) -> Token:
        tok = self._peek()
        if tok.kind != kind:
            found = tok.lexeme if tok.kind != "EOF" else "end of input"
            raise ParseError(f"expected {what}, found {found!r}", tok.start, tok.end)
        return self._advance()

    # -- statements -----------------------------------------------------

    def _statement(self):
        kind = self._peek().kind
        if kind == "LET":
            return self._let_stmt()
        if kind == "FN":
            return self._func_decl()
        if kind == "IF":
            return self._if_stmt()
        if kind == "WHILE":
            return self._while_stmt()
        if kind == "FOR":
            return self._for_stmt()
        if kind == "BREAK":
            kw = self._advance()
            semi = self._expect("SEMICOLON", "';' after 'break'")
            return BreakStmt(start=kw.start, end=semi.end)
        if kind == "CONTINUE":
            kw = self._advance()
            semi = self._expect("SEMICOLON", "';' after 'continue'")
            return ContinueStmt(start=kw.start, end=semi.end)
        if kind == "RETURN":
            return self._return_stmt()
        if kind == "THROW":
            return self._throw_stmt()
        if kind == "TRY":
            return self._try_stmt()
        if kind == "LEFT_BRACE":
            return self._block()
        return self._expr_stmt()

    def _let_stmt(self):
        kw = self._expect("LET", "'let'")
        name = self._expect("IDENT", "a variable name")
        init = None
        if self._match("EQUAL"):
            init = self._expression()
        semi = self._expect("SEMICOLON", "';' after let declaration")
        return LetStmt(start=kw.start, end=semi.end, name=name.lexeme, init=init)

    def _func_decl(self):
        kw = self._expect("FN", "'fn'")
        name = self._expect("IDENT", "a function name")
        self._expect("LEFT_PAREN", "'(' after function name")
        params = []
        if not self._check("RIGHT_PAREN"):
            params.append(self._expect("IDENT", "a parameter name").lexeme)
            while self._match("COMMA"):
                params.append(self._expect("IDENT", "a parameter name").lexeme)
        self._expect("RIGHT_PAREN", "')' after parameters")
        body = self._block()
        return FuncDecl(start=kw.start, end=body.end, name=name.lexeme, params=params, body=body)

    def _if_stmt(self):
        kw = self._expect("IF", "'if'")
        self._expect("LEFT_PAREN", "'(' after 'if'")
        cond = self._expression()
        self._expect("RIGHT_PAREN", "')' after if condition")
        then_branch = self._statement()
        else_branch = None
        if self._match("ELSE"):
            else_branch = self._statement()
        end = (else_branch or then_branch).end
        return IfStmt(start=kw.start, end=end, cond=cond,
                      then_branch=then_branch, else_branch=else_branch)

    def _while_stmt(self):
        kw = self._expect("WHILE", "'while'")
        self._expect("LEFT_PAREN", "'(' after 'while'")
        cond = self._expression()
        self._expect("RIGHT_PAREN", "')' after while condition")
        body = self._statement()
        return WhileStmt(start=kw.start, end=body.end, cond=cond, body=body)

    def _for_stmt(self):
        kw = self._expect("FOR", "'for'")
        self._expect("LEFT_PAREN", "'(' after 'for'")
        if self._match("SEMICOLON"):
            init = None
        elif self._check("LET"):
            init = self._let_stmt()
        else:
            init = self._expr_stmt()
        cond = None
        if not self._check("SEMICOLON"):
            cond = self._expression()
        self._expect("SEMICOLON", "';' after for condition")
        step = None
        if not self._check("RIGHT_PAREN"):
            step = self._expression()
        self._expect("RIGHT_PAREN", "')' after for clauses")
        body = self._statement()
        return ForStmt(start=kw.start, end=body.end, init=init, cond=cond, step=step, body=body)

    def _return_stmt(self):
        kw = self._expect("RETURN", "'return'")
        value = None
        if not self._check("SEMICOLON"):
            value = self._expression()
        semi = self._expect("SEMICOLON", "';' after return value")
        return ReturnStmt(start=kw.start, end=semi.end, value=value)

    def _throw_stmt(self):
        kw = self._expect("THROW", "'throw'")
        value = self._expression()
        semi = self._expect("SEMICOLON", "';' after throw expression")
        return ThrowStmt(start=kw.start, end=semi.end, value=value)

    def _try_stmt(self):
        kw = self._expect("TRY", "'try'")
        body = self._block()
        catch_param = None
        catch_body = None
        finally_body = None
        if self._match("CATCH"):
            self._expect("LEFT_PAREN", "'(' after 'catch'")
            catch_param = self._expect("IDENT", "a catch variable name").lexeme
            self._expect("RIGHT_PAREN", "')' after catch variable")
            catch_body = self._block()
        if self._match("FINALLY"):
            finally_body = self._block()
        if catch_body is None and finally_body is None:
            raise ParseError(
                "expected 'catch' or 'finally' after try block", body.start, body.end
            )
        end = (finally_body or catch_body).end
        return TryStmt(start=kw.start, end=end, body=body, catch_param=catch_param,
                       catch_body=catch_body, finally_body=finally_body)

    def _block(self):
        lbrace = self._expect("LEFT_BRACE", "'{'")
        stmts = []
        while not self._check("RIGHT_BRACE") and not self._at_end():
            stmts.append(self._statement())
        rbrace = self._expect("RIGHT_BRACE", "'}' after block")
        return Block(start=lbrace.start, end=rbrace.end, stmts=stmts)

    def _expr_stmt(self):
        expr = self._expression()
        semi = self._expect("SEMICOLON", "';' after expression")
        return ExprStmt(start=expr.start, end=semi.end, expr=expr)

    # -- expressions ----------------------------------------------------

    def _expression(self):
        return self._assignment()

    def _assignment(self):
        expr = self._logic_or()
        if self._match("EQUAL"):
            equals = self.tokens[self.current - 1]
            value = self._assignment()
            if isinstance(expr, Var):
                return Assign(start=expr.start, end=value.end, name=expr.name, value=value)
            raise ParseError("invalid assignment target", equals.start, equals.end)
        return expr

    def _logic_or(self):
        expr = self._logic_and()
        while self._match("OR"):
            right = self._logic_and()
            expr = Logical(start=expr.start, end=right.end, op="OR", left=expr, right=right)
        return expr

    def _logic_and(self):
        expr = self._equality()
        while self._match("AND"):
            right = self._equality()
            expr = Logical(start=expr.start, end=right.end, op="AND", left=expr, right=right)
        return expr

    def _equality(self):
        expr = self._comparison()
        while self._match("EQUAL_EQUAL", "BANG_EQUAL"):
            op = self.tokens[self.current - 1].kind
            right = self._comparison()
            expr = Binary(start=expr.start, end=right.end, op=op, left=expr, right=right)
        return expr

    def _comparison(self):
        expr = self._term()
        while self._match("LESS", "LESS_EQUAL", "GREATER", "GREATER_EQUAL"):
            op = self.tokens[self.current - 1].kind
            right = self._term()
            expr = Binary(start=expr.start, end=right.end, op=op, left=expr, right=right)
        return expr

    def _term(self):
        expr = self._factor()
        while self._match("PLUS", "MINUS"):
            op = self.tokens[self.current - 1].kind
            right = self._factor()
            expr = Binary(start=expr.start, end=right.end, op=op, left=expr, right=right)
        return expr

    def _factor(self):
        expr = self._unary()
        while self._match("STAR", "SLASH", "PERCENT"):
            op = self.tokens[self.current - 1].kind
            right = self._unary()
            expr = Binary(start=expr.start, end=right.end, op=op, left=expr, right=right)
        return expr

    def _unary(self):
        if self._match("BANG", "MINUS"):
            op = self.tokens[self.current - 1]
            operand = self._unary()
            return Unary(start=op.start, end=operand.end, op=op.kind, operand=operand)
        return self._call()

    def _call(self):
        expr = self._primary()
        while self._check("LEFT_PAREN"):
            self._advance()
            args = []
            if not self._check("RIGHT_PAREN"):
                args.append(self._expression())
                while self._match("COMMA"):
                    args.append(self._expression())
            rparen = self._expect("RIGHT_PAREN", "')' after arguments")
            expr = Call(start=expr.start, end=rparen.end, callee=expr, args=args)
        return expr

    def _primary(self):
        tok = self._peek()
        if tok.kind in ("NUMBER", "STRING"):
            self._advance()
            return Literal(start=tok.start, end=tok.end, value=tok.value)
        if tok.kind == "TRUE":
            self._advance()
            return Literal(start=tok.start, end=tok.end, value=True)
        if tok.kind == "FALSE":
            self._advance()
            return Literal(start=tok.start, end=tok.end, value=False)
        if tok.kind == "NIL":
            self._advance()
            return Literal(start=tok.start, end=tok.end, value=None)
        if tok.kind == "IDENT":
            self._advance()
            return Var(start=tok.start, end=tok.end, name=tok.lexeme)
        if tok.kind == "LEFT_PAREN":
            self._advance()
            expr = self._expression()
            self._expect("RIGHT_PAREN", "')' after expression")
            return expr
        found = tok.lexeme if tok.kind != "EOF" else "end of input"
        raise ParseError(f"expected an expression, found {found!r}", tok.start, tok.end)
