"""树遍历解释器:直接在语法树上求值,不使用 eval 或现成编译器。"""

from .nodes import (Binary, Call, ErrorNode, FuncDecl, Ident, LetDecl, Number,
                    Unary, compute_positions, walk)

MAX_CALL_DEPTH = 200


class EvalError(Exception):
    pass


def evaluate(program):
    """按顺序执行顶层声明,返回 {let 变量名: 值}。"""
    positions = compute_positions(program)
    bad = [node for node in walk(program) if isinstance(node, ErrorNode)]
    if bad:
        details = "; ".join(
            f"{node.message}(字节 {positions[id(node)][0]}..{positions[id(node)][1]})"
            for node in bad
        )
        raise EvalError(f"程序包含语法错误: {details}")
    funcs = {}
    result = {}
    for decl in program.decls:
        if isinstance(decl, FuncDecl):
            funcs[decl.name] = decl
        elif isinstance(decl, LetDecl):
            result[decl.name] = _eval(decl.value, result, funcs, positions, 0)
    return result


def _where(node, positions):
    start, end = positions[id(node)]
    return f"(字节 {start}..{end})"


def _eval(node, env, funcs, positions, depth):
    if depth > MAX_CALL_DEPTH:
        raise EvalError("调用嵌套过深(可能存在无限递归)")
    if isinstance(node, Number):
        return node.value
    if isinstance(node, Ident):
        if node.name in env:
            return env[node.name]
        raise EvalError(f"未定义的变量 '{node.name}'{_where(node, positions)}")
    if isinstance(node, Unary):
        return -_eval(node.operand, env, funcs, positions, depth)
    if isinstance(node, Binary):
        left = _eval(node.left, env, funcs, positions, depth)
        right = _eval(node.right, env, funcs, positions, depth)
        if node.op == "+":
            return left + right
        if node.op == "-":
            return left - right
        if node.op == "*":
            return left * right
        if right == 0:
            raise EvalError(f"除数为零{_where(node, positions)}")
        return left / right
    if isinstance(node, Call):
        name = node.callee.name
        decl = funcs.get(name)
        if decl is None:
            raise EvalError(f"未定义的函数 '{name}'{_where(node, positions)}")
        if len(node.args) != len(decl.params):
            raise EvalError(
                f"函数 '{name}' 需要 {len(decl.params)} 个参数,"
                f"实际传入 {len(node.args)} 个{_where(node, positions)}"
            )
        args = [_eval(arg, env, funcs, positions, depth) for arg in node.args]
        local = dict(env)
        local.update(zip(decl.params, args))
        return _eval(decl.body, local, funcs, positions, depth + 1)
    if isinstance(node, ErrorNode):
        raise EvalError(f"无法求值错误节点{_where(node, positions)}")
    raise EvalError(f"未知节点 {type(node).__name__}")
