from __future__ import annotations

from dataclasses import dataclass
from typing import Any



class ParseError(Exception):
    def __init__(self, message: str, span: tuple[int, int]):
        super().__init__(message)
        self.message = message
        self.span = span


class EvalError(Exception):
    def __init__(self, kind: str, message: str, span: tuple[int, int]):
        super().__init__(message)
        self.kind = kind
        self.message = message
        self.span = span


@dataclass(slots=True)
class Node:
    span: tuple[int, int]


@dataclass(slots=True)
class Literal(Node):
    value: Any


@dataclass(slots=True)
class Var(Node):
    name: str


@dataclass(slots=True)
class Unary(Node):
    op: str
    operand: Node


@dataclass(slots=True)
class Binary(Node):
    op: str
    left: Node
    right: Node
    op_span: tuple[int, int]


@dataclass(slots=True)
class Logical(Node):
    op: str
    left: Node
    right: Node
    op_span: tuple[int, int]


@dataclass(slots=True)
class Conditional(Node):
    test: Node
    then_expr: Node
    else_expr: Node
    q_span: tuple[int, int]
    c_span: tuple[int, int]


@dataclass(slots=True)
class Throw(Node):
    kind: str
    message: str


@dataclass(slots=True)
class Memo(Node):
    ident: str
    expr: Node


@dataclass(slots=True)
class Use(Node):
    ident: str


@dataclass(slots=True)
class Token:
    kind: str
    value: str
    span: tuple[int, int]


class Lexer:
    def __init__(self, source: str):
        self.source = source
        self.pos = 0
        self.byte_pos = 0
        self.tokens: list[Token] = []
        self.two_char = {"==", "!=", "<=", ">=", "&&", "||"}
        self.one_char = set("+-*/%!<>()?:,")

    def tokenize(self) -> list[Token]:
        while self.pos < len(self.source):
            ch = self.source[self.pos]
            if ch in " \t\r\n":
                self._advance()
                continue
            if ch == "#":
                while self.pos < len(self.source) and self.source[self.pos] != "\n":
                    self._advance()
                continue
            start_byte = self.byte_pos
            start = self.pos
            if ch.isdigit():
                self._read_number()
            elif ch.isalpha() or ch == "_":
                self._read_identifier()
            elif (
                self.source.startswith("==", self.pos)
                or self.source.startswith("!=", self.pos)
                or self.source.startswith("<=", self.pos)
                or self.source.startswith(">=", self.pos)
                or self.source.startswith("&&", self.pos)
                or self.source.startswith("||", self.pos)
            ):
                self._advance(); self._advance()
                kind = self.source[start:self.pos]
                self.tokens.append(Token(kind, kind, (start_byte, self.byte_pos)))
            elif ch in self.one_char:
                self._advance()
                self.tokens.append(Token(ch, ch, (start_byte, self.byte_pos)))
            else:
                bad_start = self.byte_pos
                self._advance()
                raise ParseError(f"unexpected character {ch!r}", (bad_start, self.byte_pos))
        end = len(self.source.encode("utf-8"))
        self.tokens.append(Token("EOF", "", (end, end)))
        return self.tokens

    def _advance(self) -> None:
        ch = self.source[self.pos]
        self.pos += 1
        self.byte_pos += len(ch.encode("utf-8"))

    def _read_number(self) -> None:
        start = self.pos
        start_byte = self.byte_pos
        while self.pos < len(self.source) and self.source[self.pos].isdigit():
            self._advance()
        text = self.source[start:self.pos]
        self.tokens.append(Token("INT", text, (start_byte, self.byte_pos)))

    def _read_identifier(self) -> None:
        start = self.pos
        start_byte = self.byte_pos
        while self.pos < len(self.source):
            ch = self.source[self.pos]
            if ch == "_" or ch.isalnum():
                self._advance()
            else:
                break
        text = self.source[start:self.pos]
        if text in {"true", "false"}:
            self.tokens.append(Token("BOOL", text, (start_byte, self.byte_pos)))
        else:
            self.tokens.append(Token("NAME", text, (start_byte, self.byte_pos)))


class Parser:
    def __init__(self, source: str):
        self.source = source
        self.tokens = Lexer(source).tokenize()
        self.index = 0

    def parse(self) -> Node:
        expr = self._conditional()
        if self._peek().kind != "EOF":
            tok = self._peek()
            raise ParseError(f"unexpected token {tok.value!r}", tok.span)
        return expr

    def _peek(self, offset: int = 0) -> Token:
        return self.tokens[min(self.index + offset, len(self.tokens) - 1)]

    def _take(self) -> Token:
        tok = self.tokens[self.index]
        self.index += 1
        return tok

    def _expect(self, kind: str) -> Token:
        tok = self._peek()
        if tok.kind != kind:
            raise ParseError(f"expected {kind!r}, got {tok.value!r}", tok.span)
        return self._take()

    def _conditional(self) -> Node:
        test = self._or()
        tok = self._peek()
        if tok.kind != "?":
            return test
        question = self._take()
        then_expr = self._conditional()
        colon = self._expect(":")
        else_expr = self._conditional()
        return Conditional(
            (test.span[0], else_expr.span[1]), test, then_expr, else_expr,
            question.span, colon.span,
        )

    def _or(self) -> Node:
        left = self._and()
        while self._peek().kind == "||":
            op = self._take()
            right = self._and()
            left = Logical((left.span[0], right.span[1]), "||", left, right, op.span)
        return left

    def _and(self) -> Node:
        left = self._equality()
        while self._peek().kind == "&&":
            op = self._take()
            right = self._equality()
            left = Logical((left.span[0], right.span[1]), "&&", left, right, op.span)
        return left

    def _equality(self) -> Node:
        left = self._relational()
        if self._peek().kind in {"==", "!="}:
            op = self._take()
            right = self._relational()
            left = Binary((left.span[0], right.span[1]), op.kind, left, right, op.span)
        if self._peek().kind in {"==", "!="}:
            tok = self._peek()
            raise ParseError("chained comparisons are not allowed", tok.span)
        return left

    def _relational(self) -> Node:
        left = self._additive()
        if self._peek().kind in {"<", ">", "<=", ">="}:
            op = self._take()
            right = self._additive()
            left = Binary((left.span[0], right.span[1]), op.kind, left, right, op.span)
        if self._peek().kind in {"<", ">", "<=", ">="}:
            tok = self._peek()
            raise ParseError("chained comparisons are not allowed", tok.span)
        return left

    def _additive(self) -> Node:
        left = self._multiplicative()
        while self._peek().kind in {"+", "-"}:
            op = self._take()
            right = self._multiplicative()
            left = Binary((left.span[0], right.span[1]), op.kind, left, right, op.span)
        return left

    def _multiplicative(self) -> Node:
        left = self._unary()
        while self._peek().kind in {"*", "/", "%"}:
            op = self._take()
            right = self._unary()
            left = Binary((left.span[0], right.span[1]), op.kind, left, right, op.span)
        return left

    def _unary(self) -> Node:
        if self._peek().kind in {"!", "-"}:
            op = self._take()
            operand = self._unary()
            return Unary((op.span[0], operand.span[1]), op.kind, operand)
        return self._primary()

    def _primary(self) -> Node:
        tok = self._peek()
        if tok.kind == "INT":
            self._take()
            return Literal(tok.span, int(tok.value))
        if tok.kind == "BOOL":
            self._take()
            return Literal(tok.span, tok.value == "true")
        if tok.kind == "NAME":
            self._take()
            return Var(tok.span, tok.value)
        if tok.kind == "(":
            open_tok = self._take()
            expr = self._conditional()
            close = self._expect(")")
            expr.span = (open_tok.span[0], close.span[1])
            return expr
        raise ParseError(f"expected an expression, got {tok.value!r}", tok.span)


def parse(source: str) -> Node:
    return Parser(source).parse()


def source_slice(source: str, span: tuple[int, int]) -> str:
    return source.encode("utf-8")[span[0]:span[1]].decode("utf-8")


def _type_error(message: str, span: tuple[int, int]) -> EvalError:
    return EvalError("TypeError", message, span)


def _zero_error(span: tuple[int, int]) -> EvalError:
    return EvalError("DivisionByZero", "division or modulo by zero", span)


def _require_int(value: Any, span: tuple[int, int]) -> int:
    if isinstance(value, bool) or not isinstance(value, int):
        raise _type_error(f"integer required, got {value!r}", span)
    return value


def _require_bool(value: Any, span: tuple[int, int]) -> bool:
    if not isinstance(value, bool):
        raise _type_error(f"boolean required, got {value!r}", span)
    return value




def _type_error(message: str, span: tuple[int, int]) -> EvalError:
    return EvalError("TypeError", message, span)


def _zero_error(span: tuple[int, int]) -> EvalError:
    return EvalError("DivisionByZero", "division or modulo by zero", span)


def _require_int(value: Any, span: tuple[int, int]) -> int:
    if isinstance(value, bool) or not isinstance(value, int):
        raise _type_error(f"integer required, got {value!r}", span)
    return value


def _require_bool(value: Any, span: tuple[int, int]) -> bool:
    if not isinstance(value, bool):
        raise _type_error(f"boolean required, got {value!r}", span)
    return value


def evaluate(node: Node, variables: dict[str, Any] | None = None) -> Any:
    env = dict(variables or {})
    memo_values: dict[str, Any] = {}

    def eval_node(current: Node, bindings: set[str]) -> Any:
        if isinstance(current, Literal):
            return current.value
        if isinstance(current, Var):
            if current.name not in env:
                raise EvalError("UndefinedVariable", f"undefined variable {current.name!r}", current.span)
            return env[current.name]
        if isinstance(current, Unary):
            value = eval_node(current.operand, bindings)
            operator_span = (current.span[0], current.span[0] + 1)
            if current.op == "-":
                return -_require_int(value, operator_span)
            return not _require_bool(value, operator_span)
        if isinstance(current, Binary):
            left = eval_node(current.left, bindings)
            right = eval_node(current.right, bindings)
            if current.op in {"==", "!="}:
                equal = left == right
                return equal if current.op == "==" else not equal
            left_num = _require_int(left, current.left.span)
            right_num = _require_int(right, current.right.span)
            if current.op == "+":
                return left_num + right_num
            if current.op == "-":
                return left_num - right_num
            if current.op == "*":
                return left_num * right_num
            if right_num == 0:
                raise _zero_error(current.op_span)
            if current.op == "/":
                return left_num // right_num
            return left_num % right_num
        if isinstance(current, Logical):
            left = eval_node(current.left, bindings)
            left_bool = _require_bool(left, current.left.span)
            if current.op == "&&":
                if not left_bool:
                    return False
                return _require_bool(eval_node(current.right, bindings), current.right.span)
            if left_bool:
                return True
            return _require_bool(eval_node(current.right, bindings), current.right.span)
        if isinstance(current, Conditional):
            condition = _require_bool(eval_node(current.test, bindings), current.test.span)
            selected = current.then_expr if condition else current.else_expr
            return eval_node(selected, set(bindings))
        if isinstance(current, Throw):
            raise EvalError(current.kind, current.message, current.span)
        if isinstance(current, Memo):
            if current.ident in bindings:
                return memo_values[current.ident]
            value = eval_node(current.expr, bindings)
            memo_values[current.ident] = value
            bindings.add(current.ident)
            return value
        if isinstance(current, Use):
            if current.ident not in bindings:
                raise RuntimeError(f"internal error: CSE value {current.ident!r} was not bound")
            return memo_values[current.ident]
        raise RuntimeError(f"internal error: unsupported node {type(current).__name__}")

    return eval_node(node, set())


PRETTY_PRECEDENCE = {
    "||": 1,
    "&&": 2,
    "==": 3,
    "!=": 3,
    "<": 4,
    ">": 4,
    "<=": 4,
    ">=": 4,
    "+": 5,
    "-": 5,
    "*": 6,
    "/": 6,
    "%": 6,
    "unary": 7,
    "conditional": 0,
}


def render(node: Node, minimum_precedence: int = 0) -> str:
    if isinstance(node, Literal):
        if node.value is True:
            return "true"
        if node.value is False:
            return "false"
        return str(node.value)
    if isinstance(node, Var):
        return node.name
    if isinstance(node, Unary):
        symbol = "-" if node.op == "-" else "!"
        text = symbol + render(node.operand, PRETTY_PRECEDENCE["unary"])
        if PRETTY_PRECEDENCE["unary"] < minimum_precedence:
            return f"({text})"
        return text
    if isinstance(node, Binary | Logical):
        precedence = PRETTY_PRECEDENCE[node.op]
        text = f"{render(node.left, precedence)} {node.op} {render(node.right, precedence + 1)}"
        if precedence < minimum_precedence:
            return f"({text})"
        return text
    if isinstance(node, Conditional):
        text = f"{render(node.test, 1)} ? {render(node.then_expr, 0)} : {render(node.else_expr, 0)}"
        if PRETTY_PRECEDENCE["conditional"] < minimum_precedence:
            return f"({text})"
        return text
    if isinstance(node, Throw):
        return f"/* throw {node.kind} at {node.span[0]}:{node.span[1]} */"
    if isinstance(node, Memo):
        return f"@{node.ident}({render(node.expr, 0)})"
    if isinstance(node, Use):
        return node.ident
    raise RuntimeError(f"internal error: unsupported node {type(node).__name__}")


def children(node: Node) -> list[Node]:
    if isinstance(node, Unary):
        return [node.operand]
    if isinstance(node, Binary | Logical):
        return [node.left, node.right]
    if isinstance(node, Conditional):
        return [node.test, node.then_expr, node.else_expr]
    if isinstance(node, Memo):
        return [node.expr]
    return []


def _constant(node: Node) -> Any | None:
    return node.value if isinstance(node, Literal) else None


def _literal(span: tuple[int, int], value: Any) -> Literal:
    return Literal(span, value)


def _constant_unary(op: str, value: Any, span: tuple[int, int]) -> Node:
    operator_span = (span[0], span[0] + 1)
    if op == "-":
        if isinstance(value, bool) or not isinstance(value, int):
            return Throw(operator_span, "TypeError", f"integer required, got {value!r}")
        return _literal(span, -value)
    if not isinstance(value, bool):
        return Throw(operator_span, "TypeError", f"boolean required, got {value!r}")
    return _literal(span, not value)


def _constant_binary(op: str, left: Any, right: Any, node: Binary) -> Node:
    if op in {"==", "!="}:
        equal = left == right
        return _literal(node.span, equal if op == "==" else not equal)
    if isinstance(left, bool) or not isinstance(left, int):
        return Throw(node.left.span, "TypeError", f"integer required, got {left!r}")
    if isinstance(right, bool) or not isinstance(right, int):
        return Throw(node.right.span, "TypeError", f"integer required, got {right!r}")
    if op == "+":
        value = left + right
    elif op == "-":
        value = left - right
    elif op == "*":
        value = left * right
    elif right == 0:
        return Throw(node.op_span, "DivisionByZero", "division or modulo by zero")
    elif op == "/":
        value = left // right
    else:
        value = left % right
    return _literal(node.span, value)


def simplify(node: Node) -> tuple[Node, list[dict[str, Any]]]:
    reasons: list[dict[str, Any]] = []

    def transform(current: Node) -> Node:
        if isinstance(current, Literal | Var | Throw):
            return current
        if isinstance(current, Unary):
            operand = transform(current.operand)
            value = _constant(operand)
            if value is not None:
                replacement = _constant_unary(current.op, value, current.span)
                reasons.append({
                    "kind": "constant_fold",
                    "span": current.span,
                    "expression": "unary",
                    "reason": f"folded unary {current.op} on literal {value!r}",
                })
                return replacement
            return Unary(current.span, current.op, operand)
        if isinstance(current, Binary):
            left = transform(current.left)
            right = transform(current.right)
            left_value = _constant(left)
            right_value = _constant(right)
            if left_value is not None and right_value is not None:
                replacement = _constant_binary(current.op, left_value, right_value, current)
                result_text = getattr(replacement, "value", f"throw:{getattr(replacement, 'kind', '')}")
                reasons.append({
                    "kind": "constant_fold",
                    "span": current.span,
                    "expression": "binary",
                    "reason": f"folded {current.op} on literals to {result_text!r}",
                })
                return replacement
            return Binary(current.span, current.op, left, right, current.op_span)
        if isinstance(current, Logical):
            left = transform(current.left)
            left_value = _constant(left)
            if left_value is not None and not isinstance(left_value, bool):
                replacement = Throw(current.left.span, "TypeError", f"boolean required, got {left_value!r}")
                reasons.append({
                    "kind": "constant_fold",
                    "span": current.span,
                    "expression": "logical",
                    "reason": f"folded logical {current.op} whose left literal is not boolean",
                })
                return replacement
            if current.op == "&&" and left_value is False:
                reasons.append({
                    "kind": "branch_simplification",
                    "span": current.span,
                    "removed_span": current.right.span,
                    "reason": "false left operand makes && short-circuit; right side is unreachable",
                })
                return Literal(current.span, False)
            if current.op == "||" and left_value is True:
                reasons.append({
                    "kind": "branch_simplification",
                    "span": current.span,
                    "removed_span": current.right.span,
                    "reason": "true left operand makes || short-circuit; right side is unreachable",
                })
                return Literal(current.span, True)
            right = transform(current.right)
            return Logical(current.span, current.op, left, right, current.op_span)
        if isinstance(current, Conditional):
            test = transform(current.test)
            test_value = _constant(test)
            if test_value is not None and not isinstance(test_value, bool):
                replacement = Throw(current.test.span, "TypeError", f"boolean required, got {test_value!r}")
                reasons.append({
                    "kind": "constant_fold",
                    "span": current.span,
                    "expression": "conditional",
                    "reason": "folded conditional whose condition literal is not boolean",
                })
                return replacement
            if isinstance(test_value, bool):
                selected, removed, branch_name = (
                    (current.then_expr, current.else_expr, "then")
                    if test_value else
                    (current.else_expr, current.then_expr, "else")
                )
                replacement = transform(selected)
                reasons.append({
                    "kind": "branch_simplification",
                    "span": current.span,
                    "selected_span": selected.span,
                    "removed_span": removed.span,
                    "reason": f"constant condition {str(test_value).lower()} selects {branch_name} branch",
                })
                return replacement
            then_expr = transform(current.then_expr)
            else_expr = transform(current.else_expr)
            return Conditional(current.span, test, then_expr, else_expr, current.q_span, current.c_span)
        if isinstance(current, Memo):
            return Memo(current.span, current.ident, transform(current.expr))
        if isinstance(current, Use):
            return current
        raise RuntimeError(f"internal error: unsupported node {type(current).__name__}")

    changed = transform(node)
    return changed, reasons


def _structural_key(node: Node) -> Any:
    if isinstance(node, Literal):
        return ("lit", node.value)
    if isinstance(node, Var):
        return ("var", node.name)
    if isinstance(node, Unary):
        return ("unary", node.op, _structural_key(node.operand))
    if isinstance(node, Binary):
        return ("binary", node.op, _structural_key(node.left), _structural_key(node.right))
    if isinstance(node, Logical):
        return ("logical", node.op, _structural_key(node.left), _structural_key(node.right))
    if isinstance(node, Conditional):
        return ("conditional", _structural_key(node.test), _structural_key(node.then_expr), _structural_key(node.else_expr))
    if isinstance(node, Throw):
        return ("throw", node.kind, node.message, node.span)
    raise RuntimeError(f"internal error: unsupported node {type(node).__name__}")


def _is_cse_candidate(node: Node) -> bool:
    return isinstance(node, Unary | Binary | Logical)


def eliminate_common_subexpressions(node: Node) -> tuple[Node, list[dict[str, Any]]]:
    counter = 0

    def worker(current: Node, available: dict[Any, str]) -> tuple[Node, dict[Any, str]]:
        nonlocal counter
        key = _structural_key(current) if _is_cse_candidate(current) else None
        if key is not None and key in available:
            ident = available[key]
            return Use(current.span, ident), available

        if isinstance(current, Literal | Var | Throw):
            return current, available
        if isinstance(current, Unary):
            operand, operand_available = worker(current.operand, available)
            transformed = Unary(current.span, current.op, operand)
            guaranteed_available = operand_available
        elif isinstance(current, Binary):
            left, left_available = worker(current.left, available)
            right, right_available = worker(current.right, dict(left_available))
            transformed = Binary(current.span, current.op, left, right, current.op_span)
            guaranteed_available = dict(right_available)
        elif isinstance(current, Logical):
            left, left_available = worker(current.left, available)
            right, _ = worker(current.right, dict(left_available))
            transformed = Logical(current.span, current.op, left, right, current.op_span)
            guaranteed_available = dict(left_available)
        elif isinstance(current, Conditional):
            test, test_available = worker(current.test, available)
            then_expr, _ = worker(current.then_expr, dict(test_available))
            else_expr, _ = worker(current.else_expr, dict(test_available))
            transformed = Conditional(current.span, test, then_expr, else_expr, current.q_span, current.c_span)
            return transformed, dict(test_available)
        else:
            raise RuntimeError(f"internal error: unsupported node {type(current).__name__}")

        if key is None:
            return transformed, guaranteed_available
        counter += 1
        ident = f"cse{counter}"
        wrapped = Memo(current.span, ident, transformed)
        outgoing = dict(guaranteed_available)
        outgoing[key] = ident
        return wrapped, outgoing

    transformed, _ = worker(node, {})

    used: set[str] = set()

    def collect_used(current: Node) -> None:
        if isinstance(current, Use):
            used.add(current.ident)
        for child in children(current):
            collect_used(child)

    def strip(current: Node) -> Node:
        if isinstance(current, Memo):
            stripped_expr = strip(current.expr)
            return stripped_expr if current.ident not in used else Memo(current.span, current.ident, stripped_expr)
        if isinstance(current, Use):
            return current
        if isinstance(current, Unary):
            return Unary(current.span, current.op, strip(current.operand))
        if isinstance(current, Binary):
            return Binary(current.span, current.op, strip(current.left), strip(current.right), current.op_span)
        if isinstance(current, Logical):
            return Logical(current.span, current.op, strip(current.left), strip(current.right), current.op_span)
        if isinstance(current, Conditional):
            return Conditional(current.span, strip(current.test), strip(current.then_expr), strip(current.else_expr), current.q_span, current.c_span)
        return current

    collect_used(transformed)
    transformed = strip(transformed)

    reasons: list[dict[str, Any]] = []

    def emit_reasons(current: Node) -> None:
        if isinstance(current, Memo):
            reasons.append({
                "kind": "common_subexpression_elimination",
                "mode": "binding",
                "ident": current.ident,
                "span": current.span,
                "reason": "first evaluation is bound because the same pure expression is evaluated later in the same reachable scope",
            })
            emit_reasons(current.expr)
        elif isinstance(current, Use):
            reasons.append({
                "kind": "common_subexpression_elimination",
                "mode": "use",
                "ident": current.ident,
                "span": current.span,
                "reason": "reuses the previously evaluated value without changing operand order or short-circuiting",
            })
        else:
            for child in children(current):
                emit_reasons(child)

    emit_reasons(transformed)
    return transformed, reasons


def optimize(source: str) -> dict[str, Any]:
    original = parse(source)
    simplified, simplify_reasons = simplify(original)
    optimized, cse_reasons = eliminate_common_subexpressions(simplified)

    prefixes = {
        "constant_fold": "cf",
        "branch_simplification": "bs",
        "common_subexpression_elimination": "cse",
    }
    counts = {kind: 0 for kind in prefixes}
    ordered: list[dict[str, Any]] = []
    def normalize(value):
        if isinstance(value, tuple):
            return [normalize(item) for item in value]
        if isinstance(value, list):
            return [normalize(item) for item in value]
        if isinstance(value, dict):
            return {key: normalize(item) for key, item in value.items()}
        return value

    for entry in [*simplify_reasons, *cse_reasons]:
        counts[entry["kind"]] += 1
        ident = f"{prefixes[entry['kind']]}-{counts[entry['kind']]}"
        ordered.append(normalize({"id": ident, **entry}))

    by_id = {entry["id"]: entry for entry in ordered}
    by_span: dict[str, list[str]] = {}
    by_kind: dict[str, list[str]] = {kind: [] for kind in prefixes}
    for entry in ordered:
        start_byte, end_byte = entry["span"]
        interval = f"{start_byte}:{end_byte}"
        by_span.setdefault(interval, []).append(entry["id"])
        by_kind[entry["kind"]].append(entry["id"])

    return {
        "source": source,
        "original_ast": original,
        "optimized_ast": optimized,
        "optimized": render(optimized),
        "reasons": {
            "ordered": ordered,
            "by_id": by_id,
            "by_span": by_span,
            "by_kind": by_kind,
        },
    }
