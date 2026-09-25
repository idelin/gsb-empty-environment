from __future__ import annotations

import sys
from dataclasses import dataclass
from typing import Optional, Union


Value = Union[None, bool, int, str]


class LangError(Exception):
    def __init__(self, message: str, span: "Span"):
        super().__init__(message)
        self.span = span
        self.trace: Optional[list["TraceNode"]] = None

    def format(self, source: str) -> str:
        line, column = source_span_to_line_column(source, self.span)
        text = (
            f"{self.args[0]} at line {line}, column {column} "
            f"(UTF-8 bytes {self.span.start}-{self.span.end})"
        )
        if self.trace:
            text += "\nlogical call trace:\n" + format_trace(self.trace, source)
        return text


@dataclass(frozen=True)
class Span:
    start: int
    end: int


def source_span_to_line_column(source: str, span: Span) -> tuple[int, int]:
    line = source.count("\n", 0, span.start) + 1
    line_start = source.rfind("\n", 0, span.start) + 1
    column = len(source[line_start:span.start].encode("utf-8")) + 1
    return line, column


def slice_span(source: str, span: Span) -> str:
    return source.encode("utf-8")[span.start:span.end].decode("utf-8")


class Lexer:
    SINGLE = {"(": "LPAREN", ")": "RPAREN", "{": "LBRACE", "}": "RBRACE",
              ";": "SEMI", ",": "COMMA", "+": "PLUS", "-": "MINUS",
              "*": "STAR", "/": "SLASH", "%": "PERCENT", "!": "BANG",
              "<": "LT", ">": "GT", "=": "EQ"}
    WORDS = {"fun": "FUN", "if": "IF", "else": "ELSE", "return": "RETURN",
             "try": "TRY", "finally": "FINALLY", "true": "TRUE",
             "false": "FALSE", "null": "NULL", "&&": "AND", "||": "OR",
             "==": "EQEQ", "!=": "BANGEQ", "<=": "LTE", ">=": "GTE"}

    def __init__(self, source: str):
        self.source = source
        self.data = source.encode("utf-8")
        self.pos = 0
        self.tokens: list["Token"] = []

    def tokenize(self) -> list["Token"]:
        while self.pos < len(self.data):
            byte = self.data[self.pos]
            if byte in b" \t\r\n":
                self.pos += 1
            elif self.data.startswith(b"//", self.pos):
                while self.pos < len(self.data) and self.data[self.pos] != 10:
                    self.pos += 1
            elif byte == 34:
                self.read_string()
            elif 48 <= byte <= 57:
                self.read_number()
            elif self.is_ident_start(byte):
                self.read_identifier()
            else:
                self.read_operator()
        self.tokens.append(Token("EOF", None, Span(self.pos, self.pos)))
        return self.tokens

    def is_ident_start(self, byte: int) -> bool:
        return byte == 95 or 65 <= byte <= 90 or 97 <= byte <= 122 or byte >= 128

    def is_ident_part(self, byte: int) -> bool:
        return self.is_ident_start(byte) or 48 <= byte <= 57

    def add(self, kind: str, value: Optional[str], start: int, end: Optional[int] = None):
        self.tokens.append(Token(kind, value, Span(start, self.pos if end is None else end)))

    def read_number(self):
        start = self.pos
        while self.pos < len(self.data) and 48 <= self.data[self.pos] <= 57:
            self.pos += 1
        text = self.data[start:self.pos].decode("utf-8")
        self.add("INT", text, start)

    def read_identifier(self):
        start = self.pos
        while self.pos < len(self.data) and self.is_ident_part(self.data[self.pos]):
            self.pos += 1
        text = self.data[start:self.pos].decode("utf-8")
        self.add(self.WORDS.get(text, "IDENT"), text, start)

    def read_string(self):
        start = self.pos
        self.pos += 1
        chars: list[str] = []
        while self.pos < len(self.data) and self.data[self.pos] != 34:
            if self.data[self.pos] == 92:
                self.pos += 1
                if self.pos >= len(self.data):
                    raise LangError("unterminated string", Span(start, len(self.data)))
                esc = self.data[self.pos]
                chars.append({110: "\n", 116: "\t", 114: "\r",
                              92: "\\", 34: '"'}.get(esc, chr(esc)))
                self.pos += 1
            else:
                begin = self.pos
                self.advance_utf8()
                chars.append(self.data[begin:self.pos].decode("utf-8"))
        if self.pos >= len(self.data):
            raise LangError("unterminated string", Span(start, len(self.data)))
        self.pos += 1
        self.add("STRING", "".join(chars), start)

    def advance_utf8(self):
        byte = self.data[self.pos]
        if byte < 128:
            self.pos += 1
        elif byte < 224:
            self.pos += 2
        elif byte < 240:
            self.pos += 3
        else:
            self.pos += 4

    def read_operator(self):
        start = self.pos
        for symbol in ("==", "!=", "<=", ">=", "&&", "||"):
            if self.data.startswith(symbol.encode("utf-8"), self.pos):
                self.pos += len(symbol)
                self.add(self.WORDS[symbol], symbol, start)
                return
        ch = chr(self.data[self.pos])
        if ch not in self.SINGLE:
            raise LangError(f"unexpected character {ch!r}", Span(start, start + 1))
        self.pos += 1
        self.add(self.SINGLE[ch], ch, start)


@dataclass(frozen=True)
class Token:
    kind: str
    value: Optional[str]
    span: Span


@dataclass(frozen=True)
class Node:
    span: Span


@dataclass(frozen=True)
class Int(Node):
    value: int


@dataclass(frozen=True)
class Str(Node):
    value: str


@dataclass(frozen=True)
class Bool(Node):
    value: bool


@dataclass(frozen=True)
class Null(Node):
    pass


@dataclass(frozen=True)
class Name(Node):
    name: str


@dataclass(frozen=True)
class Unary(Node):
    op: str
    value: Node


@dataclass(frozen=True)
class Binary(Node):
    op: str
    left: Node
    right: Node


@dataclass(frozen=True)
class Logical(Node):
    op: str
    left: Node
    right: Node


@dataclass(frozen=True)
class Call(Node):
    name: str
    args: list[Node]
    is_tail: bool = False
    blocked_reason: Optional[str] = None


@dataclass(frozen=True)
class If(Node):
    test: Node
    then: Node
    otherwise: Node


@dataclass(frozen=True)
class Try(Node):
    body: Node
    final: Node


@dataclass(frozen=True)
class Param:
    name: str
    span: Span


@dataclass(frozen=True)
class Function(Node):
    name: str
    params: list[Param]
    body: list[Node]
    result: Node
    tail_calls: tuple["Call", ...] = ()


@dataclass(frozen=True)
class Program(Node):
    functions: tuple[Function, ...]
    statements: tuple[Node, ...]
    result: Node


class Parser:
    def __init__(self, source: str):
        self.source = source
        self.tokens = Lexer(source).tokenize()
        self.index = 0

    @property
    def current(self) -> Token:
        return self.tokens[self.index]

    def eat(self, kind: str) -> Token:
        token = self.current
        if token.kind != kind:
            raise LangError(f"expected {kind}, got {token.kind}", token.span)
        self.index += 1
        return token

    def accept(self, kind: str) -> Optional[Token]:
        if self.current.kind == kind:
            return self.eat(kind)
        return None

    def parse(self) -> Program:
        start = self.current.span.start
        functions: list[Function] = []
        statements: list[Node] = []
        while self.current.kind != "EOF":
            if self.current.kind == "FUN":
                functions.append(self.function())
            else:
                statements.append(self.statement())
        result = Null(Span(self.current.span.start, self.current.span.start))
        if statements and isinstance(statements[-1], Return):
            result = statements.pop().value
        program = Program(Span(start, self.current.span.start),
                          tuple(functions), tuple(statements), result)
        return mark_program(program)

    def function(self) -> Function:
        start = self.eat("FUN").span.start
        name = self.eat("IDENT").value
        assert name is not None
        self.eat("LPAREN")
        params: list[Param] = []
        if self.current.kind != "RPAREN":
            while True:
                token = self.eat("IDENT")
                params.append(Param(token.value or "", token.span))
                if not self.accept("COMMA"):
                    break
        self.eat("RPAREN")
        self.eat("LBRACE")
        body, result = self.block_body()
        end = self.eat("RBRACE").span.end
        return Function(Span(start, end), name, params, body, result)

    def block_body(self) -> tuple[list[Node], Node]:
        statements: list[Node] = []
        while self.current.kind not in ("RBRACE", "EOF"):
            item = self.statement()
            if isinstance(item, Return):
                if self.current.kind != "RBRACE":
                    raise LangError("statements after return are not supported",
                                    self.current.span)
                return [], item.value
            statements.append(item)
        if not statements:
            raise LangError("block needs a return expression", self.current.span)
        result = statements.pop()
        return statements, result

    def statement(self) -> Node:
        if self.current.kind == "IF":
            return self.if_statement()
        if self.current.kind == "RETURN":
            token = self.eat("RETURN")
            value = self.expression()
            end = self.eat("SEMI").span.end
            return Return(Span(token.span.start, end), value)
        value = self.expression()
        self.eat("SEMI")
        return value

    def expression(self) -> Node:
        return self.assignment()

    def assignment(self) -> Node:
        return self.logic_or()

    def logic_or(self) -> Node:
        node = self.logic_and()
        while self.current.kind == "OR":
            token = self.eat("OR")
            right = self.logic_and()
            node = Logical(Span(node.span.start, right.span.end), "||", node, right)
        return node

    def logic_and(self) -> Node:
        node = self.equality()
        while self.current.kind == "AND":
            token = self.eat("AND")
            right = self.equality()
            node = Logical(Span(node.span.start, right.span.end), "&&", node, right)
        return node

    def equality(self) -> Node:
        node = self.comparison()
        while self.current.kind in ("EQEQ", "BANGEQ"):
            token = self.tokens[self.index]
            self.index += 1
            right = self.comparison()
            node = Binary(Span(node.span.start, right.span.end),
                          "==" if token.kind == "EQEQ" else "!=", node, right)
        return node

    def comparison(self) -> Node:
        node = self.term()
        while self.current.kind in ("LT", "LTE", "GT", "GTE"):
            token = self.tokens[self.index]
            self.index += 1
            right = self.term()
            op = {"LT": "<", "LTE": "<=", "GT": ">", "GTE": ">="}[token.kind]
            node = Binary(Span(node.span.start, right.span.end), op, node, right)
        return node

    def term(self) -> Node:
        node = self.factor()
        while self.current.kind in ("PLUS", "MINUS"):
            token = self.tokens[self.index]
            self.index += 1
            right = self.factor()
            op = "+" if token.kind == "PLUS" else "-"
            node = Binary(Span(node.span.start, right.span.end), op, node, right)
        return node

    def factor(self) -> Node:
        node = self.unary()
        while self.current.kind in ("STAR", "SLASH", "PERCENT"):
            token = self.tokens[self.index]
            self.index += 1
            right = self.unary()
            op = {"STAR": "*", "SLASH": "/", "PERCENT": "%"}[token.kind]
            node = Binary(Span(node.span.start, right.span.end), op, node, right)
        return node

    def unary(self) -> Node:
        if self.current.kind in ("BANG", "MINUS"):
            token = self.tokens[self.index]
            self.index += 1
            value = self.unary()
            op = "!" if token.kind == "BANG" else "-"
            return Unary(Span(token.span.start, value.span.end), op, value)
        return self.call()

    def call(self) -> Node:
        if self.current.kind != "IDENT":
            return self.primary()
        token = self.eat("IDENT")
        if self.current.kind != "LPAREN":
            return Name(token.span, token.value or "")
        self.eat("LPAREN")
        args: list[Node] = []
        if self.current.kind != "RPAREN":
            while True:
                args.append(self.expression())
                if not self.accept("COMMA"):
                    break
        end = self.eat("RPAREN").span.end
        return Call(Span(token.span.start, end), token.value or "", args)

    def primary(self) -> Node:
        token = self.current
        if token.kind == "INT":
            self.eat("INT")
            return Int(token.span, int(token.value or 0))
        if token.kind == "STRING":
            self.eat("STRING")
            return Str(token.span, token.value or "")
        if token.kind == "TRUE":
            self.eat("TRUE")
            return Bool(token.span, True)
        if token.kind == "FALSE":
            self.eat("FALSE")
            return Bool(token.span, False)
        if token.kind == "NULL":
            self.eat("NULL")
            return Null(token.span)
        if token.kind == "IDENT":
            self.eat("IDENT")
            return Name(token.span, token.value or "")
        if token.kind == "LPAREN":
            self.eat("LPAREN")
            value = self.expression()
            self.eat("RPAREN")
            return value
        if token.kind == "IF":
            return self.if_expression()
        if token.kind == "TRY":
            return self.try_expression()
        raise LangError(f"unexpected token {token.kind}", token.span)

    def if_expression(self) -> Node:
        start = self.eat("IF").span.start
        test = self.expression()
        then = self.if_branch_expression()
        self.eat("ELSE")
        otherwise = self.expression()
        return If(Span(start, otherwise.span.end), test, then, otherwise)

    def if_statement(self) -> Node:
        start = self.eat("IF").span.start
        test = self.expression()
        then = self.if_branch_result()
        self.eat("ELSE")
        otherwise = self.if_branch_result()
        self.eat("SEMI")
        return If(Span(start, self.tokens[self.index - 1].span.end),
                  test, then, otherwise)

    def if_branch_expression(self) -> Node:
        return self.expression()

    def if_branch_result(self) -> Node:
        if self.current.kind == "RETURN":
            token = self.eat("RETURN")
            value = self.expression()
            return Return(Span(token.span.start, value.span.end), value)
        value = self.expression()
        self.eat("SEMI")
        return value

    def try_expression(self) -> Node:
        start = self.eat("TRY").span.start
        body = self.expression()
        self.eat("FINALLY")
        final = self.expression()
        return Try(Span(start, final.span.end), body, final)


@dataclass(frozen=True)
class Return(Node):
    value: Node


def mark_program(program: Program) -> Program:
    functions = tuple(mark_function(function) for function in program.functions)
    statements = tuple(mark_tail(statement, False) for statement in program.statements)
    result = mark_tail(program.result, True)
    return Program(program.span, functions, statements, result)


def mark_function(function: Function) -> Function:
    calls: list[Call] = []
    result = mark_tail(function.result, True, calls)
    body: list[Node] = []
    for statement in function.body:
        if isinstance(statement, Return):
            body.append(Return(statement.span,
                               mark_tail(statement.value, True, calls)))
        else:
            body.append(mark_tail(statement, False, calls))
    return Function(function.span, function.name, function.params, body,
                    result, tuple(calls))


def mark_tail(node: Node, tail: bool, calls: Optional[list[Call]] = None,
              blocked: Optional[str] = None) -> Node:
    if calls is None:
        calls = []
    if isinstance(node, Call):
        reason = None if tail else (
            blocked or "call has pending work or is not in return position"
        )
        marked = Call(node.span, node.name,
                      [mark_tail(arg, False, calls,
                                 "arguments must be evaluated before the call")
                       for arg in node.args],
                      tail, reason)
        if tail:
            calls.append(marked)
        return marked
    if isinstance(node, Int | Str | Bool | Null | Name):
        return node
    if isinstance(node, Unary):
        return Unary(node.span, node.op,
                     mark_tail(node.value, False, calls,
                               "a unary operation must finish after the call"))
    if isinstance(node, Binary):
        return Binary(node.span, node.op,
                      mark_tail(node.left, False, calls,
                                "a binary operation still needs this value"),
                      mark_tail(node.right, False, calls,
                                "a pending binary operation must finish"))
    if isinstance(node, Logical):
        return Logical(node.span, node.op,
                       mark_tail(node.left, False, calls,
                                 "a logical operation still needs this value"),
                       mark_tail(node.right, False, calls,
                                 "a pending logical operation must finish"))
    if isinstance(node, If):
        return If(node.span, mark_tail(node.test, False, calls),
                  mark_tail(node.then, tail, calls, blocked),
                  mark_tail(node.otherwise, tail, calls, blocked))
    if isinstance(node, Try):
        body = mark_tail(node.body, False, calls,
                         "the finally block must run after this call")
        final = mark_tail(node.final, False, calls,
                          "cleanup inside finally cannot replace the caller frame")
        return Try(node.span, body, final)
    if isinstance(node, Return):
        return Return(node.span, mark_tail(node.value, tail, calls, blocked))
    raise AssertionError(f"unhandled node {type(node)}")


@dataclass
class Instruction:
    op: str
    span: Span
    a: int = -1
    b: int = -1


@dataclass
class ExceptionHandler:
    body_start: int
    body_end: int
    finally_start: int
    stack_depth: int
    exception_slot: int


@dataclass
class Code:
    name: str
    arity: int
    params: tuple[str, ...]
    instructions: list[Instruction]
    handlers: list[ExceptionHandler]
    constants: list[object]
    slot_count: int
    tail_call_count: int = 0


class Compiler:
    BUILTINS = {"print": -1, "fail": -1, "frame_id": 0, "frames": 0}

    def __init__(self, program: Program):
        self.program = program
        self.functions = {function.name: function for function in program.functions}

    def compile(self) -> dict[str, Code]:
        codes = {name: self.compile_function(function)
                 for name, function in self.functions.items()}
        codes["__main__"] = self.compile_main()
        return codes

    def compile_function(self, function: Function) -> Code:
        compiler = ChunkCompiler(function.name, function.params, self.functions)
        for statement in function.body:
            compiler.compile_statement(statement)
        compiler.compile(function.result)
        compiler.emit("RETURN", function.result.span)
        return compiler.finish(function.params, len(function.params),
                               len(function.tail_calls))

    def compile_main(self) -> Code:
        compiler = ChunkCompiler("__main__", (), self.functions)
        for statement in self.program.statements:
            compiler.compile_statement(statement)
        compiler.compile(self.program.result)
        compiler.emit("RETURN", self.program.result.span)
        return compiler.finish((), 0, 0)


class ChunkCompiler:
    def __init__(self, name: str, params: tuple[str | Param, ...],
                 functions: dict[str, Function]):
        self.name = name
        self.functions = functions
        self.instructions: list[Instruction] = []
        self.handlers: list[ExceptionHandler] = []
        self.constants: list[object] = []
        self.locals: dict[str, int] = {}
        self.stack_depth = 0
        for param in params:
            param_name = param.name if isinstance(param, Param) else param
            self.locals[param_name] = len(self.locals)
        self.stack_depth = 0

    def emit(self, op: str, span: Span, a: int = -1, b: int = -1) -> int:
        index = len(self.instructions)
        self.instructions.append(Instruction(op, span, a, b))
        if op in ("CONST", "LOAD"):
            self.stack_depth += 1
        elif op in ("POP", "STORE", "UNARY", "BINARY"):
            self.stack_depth -= 1
        elif op in ("CALL", "BUILTIN"):
            self.stack_depth += 1 - b
        return index

    def constant(self, value: object) -> int:
        for index, existing in enumerate(self.constants):
            if existing is value:
                return index
        self.constants.append(value)
        return len(self.constants) - 1

    def temporary(self, prefix: str) -> int:
        name = f"{prefix}#{len(self.locals)}"
        self.locals[name] = len(self.locals)
        return self.locals[name]

    def finish(self, params, arity: int, tail_calls: int) -> Code:
        return Code(self.name, arity,
                    tuple(param.name if isinstance(param, Param) else param
                          for param in params),
                    self.instructions, self.handlers, self.constants,
                    len(self.locals), tail_calls)

    def compile_statement(self, node: Node):
        self.compile(node)
        self.emit("POP", node.span)

    def compile(self, node: Node):
        if isinstance(node, (Return,)):
            self.compile(node.value)
            self.emit("RETURN", node.span)
        elif isinstance(node, Int):
            self.emit("CONST", node.span, self.constant(node.value))
        elif isinstance(node, Str):
            self.emit("CONST", node.span, self.constant(node.value))
        elif isinstance(node, Bool):
            self.emit("CONST", node.span, self.constant(node.value))
        elif isinstance(node, Null):
            self.emit("CONST", node.span, self.constant(None))
        elif isinstance(node, Name):
            if node.name not in self.locals:
                raise LangError(f"unknown name {node.name!r}", node.span)
            self.emit("LOAD", node.span, self.locals[node.name])
        elif isinstance(node, Unary):
            self.compile(node.value)
            self.emit("UNARY", node.span, self.constant(node.op))
        elif isinstance(node, Binary):
            self.compile(node.left)
            self.compile(node.right)
            self.emit("BINARY", node.span, self.constant(node.op))
        elif isinstance(node, Logical):
            self.compile(node.left)
            jump_kind = "JUMP_IF_FALSE" if node.op == "&&" else "JUMP_IF_TRUE"
            jump = self.emit(jump_kind, node.span)
            pop = self.emit("POP", node.left.span)
            self.instructions[jump].a = pop
            self.compile(node.right)
        elif isinstance(node, If):
            self.compile(node.test)
            jump_else = self.emit("JUMP_IF_FALSE", node.span)
            self.emit("POP", node.test.span)
            self.compile(node.then)
            jump_end = self.emit("JUMP", node.span)
            else_index = len(self.instructions)
            self.compile(node.otherwise)
            end_index = len(self.instructions)
            self.instructions[jump_else].a = else_index
            self.instructions[jump_end].a = end_index
        elif isinstance(node, Try):
            self.compile_try(node)
        elif isinstance(node, Call):
            self.compile_call(node)
        else:
            raise AssertionError(f"unhandled node {type(node)}")

    def compile_call(self, node: Call):
        for arg in node.args:
            self.compile(arg)
        builtin = Compiler.BUILTINS.get(node.name)
        if builtin is not None:
            self.emit("BUILTIN", node.span, self.constant(node.name), len(node.args))
        else:
            function = self.functions.get(node.name)
            if function is None:
                raise LangError(f"unknown function {node.name!r}", node.span)
            if len(node.args) != len(function.params):
                raise LangError(
                    f"function {node.name!r} expects {len(function.params)} arguments, "
                    f"got {len(node.args)}", node.span)
            name_slot = self.constant(function.name)
            op = "TAIL_CALL" if node.is_tail else "CALL"
            self.emit(op, node.span, name_slot, len(node.args))

    def compile_try(self, node: Try):
        result_slot = self.temporary("try_result")
        exception_slot = self.temporary("try_exception")
        body_start = len(self.instructions)
        entry_depth = self.stack_depth
        self.compile(node.body)
        body_end = len(self.instructions) - 1
        self.emit("STORE", node.span, result_slot)
        finally_start = len(self.instructions)
        self.compile(node.final)
        self.emit("POP", node.final.span)
        self.emit("LOAD", node.span, exception_slot)
        self.emit("FINISH_TRY", node.span, result_slot, exception_slot)
        self.handlers.append(ExceptionHandler(
            body_start, body_end, finally_start, entry_depth, exception_slot))


@dataclass
class TraceNode:
    call_number: int
    frame_number: int
    function: str
    span: Span
    tail: bool
    previous: "TraceNode | None" = None


@dataclass
class Frame:
    number: int
    code: Code
    slots: list[object]
    stack: list[object]
    ip: int = 0
    trace: TraceNode | None = None


def is_truthy(value: Value) -> bool:
    if value is None or value is False or value == 0:
        return False
    return True


def show_value(value: Value) -> str:
    if value is None:
        return "null"
    if value is True:
        return "true"
    if value is False:
        return "false"
    if isinstance(value, str):
        return value
    return str(value)


def chain_depth(node: TraceNode | None) -> int:
    depth = 0
    while node is not None:
        depth += 1
        node = node.previous
    return depth


def chain_tail(node: TraceNode | None) -> list[TraceNode]:
    result: list[TraceNode] = []
    while node is not None:
        result.append(node)
        node = node.previous
    result.reverse()
    return result


def logical_trace(frames: list[Frame], limit: int = 20) -> list[TraceNode]:
    chain: list[TraceNode] = []
    for frame in frames:
        chain.extend(chain_tail(frame.trace)[1:] if chain else chain_tail(frame.trace))
    if limit and len(chain) > limit:
        kept_head = limit // 2
        kept_tail = limit - kept_head
        return chain[:kept_head] + chain[-kept_tail:]
    return chain


def format_trace(nodes: list[TraceNode], source: str, limit: int = 20) -> str:
    shown = nodes[-limit:] if len(nodes) > limit else nodes
    lines = []
    for index, node in enumerate(shown):
        if index == 0 and len(nodes) > limit:
            lines.append(f"  ... {len(nodes) - limit} earlier logical calls omitted ...")
        text = slice_span(source, node.span)
        kind = "tail" if node.tail else "call"
        lines.append(
            f"  #{node.call_number} frame#{node.frame_number} {node.function} "
            f"[{kind}] bytes {node.span.start}-{node.span.end}: {text}"
        )
    return "\n".join(lines)


class VM:
    def __init__(self, codes: dict[str, Code], source: str):
        self.codes = codes
        self.source = source
        self.frames: list[Frame] = []
        self.next_frame_number = 1
        self.next_call_number = 1
        self.max_physical_frames = 0
        self.max_logical_depth = 0

    def run(self) -> Value:
        code = self.codes["__main__"]
        node = TraceNode(0, self.next_frame_number, "__main__",
                         Span(0, len(self.source.encode("utf-8"))), False)
        main = Frame(self.next_frame_number, code, [None] * code.slot_count,
                     [], 0, node)
        self.next_frame_number += 1
        self.frames.append(main)
        self.max_physical_frames = 1
        pending: LangError | None = None
        while True:
            if pending is not None:
                handler, frame = self.find_handler()
                if handler is None:
                    if frame is None:
                        raise RuntimeError("empty VM frame stack")
                    popped = self.frames.pop()
                    pending.trace = logical_trace(self._frames_with_node(popped))
                    raise pending
                frame.ip = handler.finally_start
                del frame.stack[handler.stack_depth:]
                frame.slots[handler.exception_slot] = pending
                frame.stack.append(None)
                pending = None
                continue
            if not self.frames:
                return main.stack[0]
            frame = self.frames[-1]
            if frame.ip >= len(frame.code.instructions):
                raise RuntimeError("ran past code")
            instruction = frame.code.instructions[frame.ip]
            frame.ip += 1
            try:
                self.execute(instruction, frame)
            except LangError as error:
                pending = self.attach_position(error, instruction.span)
            except ZeroDivisionError:
                pending = LangError("division by zero", instruction.span)
    def _frames_with_node(self, popped: Frame) -> list[Frame]:
        return self.frames + [popped]

    def attach_position(self, error: LangError, span: Span) -> LangError:
        if error.span.start == error.span.end:
            error.span = span
        return error

    def find_handler(self) -> tuple[ExceptionHandler | None, Frame | None]:
        for frame in reversed(self.frames):
            for handler in reversed(frame.code.handlers):
                if handler.body_start <= frame.ip <= handler.body_end + 1:
                    return handler, frame
        return None, self.frames[-1] if self.frames else None

    def execute(self, instruction: Instruction, frame: Frame):
        code = frame.code
        op = instruction.op
        if op == "CONST":
            frame.stack.append(code.constants[instruction.a])
        elif op == "LOAD":
            frame.stack.append(frame.slots[instruction.a])
        elif op == "STORE":
            frame.slots[instruction.a] = frame.stack.pop()
        elif op == "POP":
            frame.stack.pop()
        elif op == "UNARY":
            right = frame.stack.pop()
            frame.stack.append(self.unary(code.constants[instruction.a], right, instruction.span))
        elif op == "BINARY":
            right = frame.stack.pop()
            left = frame.stack.pop()
            frame.stack.append(self.binary(code.constants[instruction.a], left, right,
                                           instruction.span))
        elif op in ("JUMP_IF_FALSE", "JUMP_IF_TRUE"):
            value = is_truthy(frame.stack[-1])
            wanted = op == "JUMP_IF_TRUE"
            if value == wanted:
                frame.ip = instruction.a
        elif op == "JUMP":
            frame.ip = instruction.a
        elif op == "FINISH_TRY":
            frame.stack.pop()
            error = frame.slots[instruction.b]
            result = frame.slots[instruction.a]
            frame.stack.append(result)
            if error is not None:
                raise error
        elif op == "CALL":
            self.call_user(code.constants[instruction.a], instruction.b,
                           instruction.span, frame, tail=False)
        elif op == "TAIL_CALL":
            self.call_user(code.constants[instruction.a], instruction.b,
                           instruction.span, frame, tail=True)
        elif op == "BUILTIN":
            args = [frame.stack.pop() for _ in range(instruction.b)][::-1]
            frame.stack.append(self.builtin(code.constants[instruction.a], args,
                                            instruction.span, frame))
        elif op == "RETURN":
            value = frame.stack.pop()
            current = self.frames.pop()
            if not self.frames:
                current.stack.append(value)
                return value
            self.frames[-1].stack.append(value)
        else:
            raise RuntimeError(f"unknown opcode {op}")

    def call_user(self, function_name: str, argc: int, span: Span,
                  caller: Frame, tail: bool):
        callee = self.codes[function_name]
        args = [caller.stack.pop() for _ in range(argc)][::-1]
        previous = caller.trace
        node = TraceNode(self.next_call_number, self.next_frame_number,
                         function_name, span, tail, previous)
        self.next_call_number += 1
        slots = list(args) + [None] * max(0, callee.slot_count - argc)
        if tail:
            caller.code = callee
            caller.slots = slots
            caller.stack = []
            caller.ip = 0
            caller.trace = node
            target = caller
        else:
            target = Frame(self.next_frame_number, callee, slots, [], 0, node)
            self.next_frame_number += 1
            self.frames.append(target)
        self.max_physical_frames = max(self.max_physical_frames, len(self.frames))
        depth = chain_depth(target.trace)
        self.max_logical_depth = max(self.max_logical_depth, depth)

    def builtin(self, name: str, args: list[Value], span: Span,
                frame: Frame) -> Value:
        if name == "print":
            print(" ".join(show_value(arg) for arg in args))
            return None
        if name == "fail":
            message = args[0] if args else "failure"
            raise LangError(show_value(message), span)
        if name == "frame_id":
            return frame.number
        if name == "frames":
            return len(self.frames)
        raise LangError(f"unknown builtin {name!r}", span)

    def unary(self, op: object, value: Value, span: Span) -> Value:
        if op == "!":
            return not is_truthy(value)
        if op == "-":
            if not isinstance(value, int):
                raise LangError("unary - requires an integer", span)
            return -value
        raise RuntimeError(f"unknown unary {op}")

    def binary(self, op: object, left: Value, right: Value, span: Span) -> Value:
        if op in ("&&", "||"):
            raise RuntimeError("logical op must be short-circuited")
        if op in ("==", "!="):
            equal = left == right
            return equal if op == "==" else not equal
        if op in ("<", "<=", ">", ">="):
            if not isinstance(left, int) or not isinstance(right, int):
                raise LangError(f"operator {op} requires integers", span)
            return {"<": left < right, "<=": left <= right,
                    ">": left > right, ">=": left >= right}[op]
        if not isinstance(left, int) or not isinstance(right, int):
            raise LangError(f"operator {op} requires integers", span)
        if op == "+":
            return left + right
        if op == "-":
            return left - right
        if op == "*":
            return left * right
        if op == "/":
            return left // right
        if op == "%":
            return left % right
        raise RuntimeError(f"unknown binary {op}")


def parse(source: str) -> Program:
    return Parser(source).parse()


def compile_program(program: Program) -> dict[str, Code]:
    return Compiler(program).compile()


def execute_source(source: str) -> tuple[Value, VM]:
    program = parse(source)
    codes = compile_program(program)
    vm = VM(codes, source)
    value = vm.run()
    return value, vm
