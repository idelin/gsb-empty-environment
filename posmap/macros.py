"""文本级宏展开，在 token 流上携带"来源帧"。

Frame.kind:
  - user:         直接来自用户原文
  - macro_body:   宏定义体中的 token（生成片段），附 call_site/def_span
  - call_context: 作为宏实参的 token，附外层调用处与宏定义

一个实参在宏体中使用多次会产生多个指向同一源区间的 PToken（一对多）；
嵌套宏使来源帧叠加，来源区间互相重叠。
"""
from __future__ import annotations

from dataclasses import dataclass

from .intervals import Span
from .lexer import Token, lex


class MacroError(Exception):
    pass


@dataclass(frozen=True)
class Frame:
    kind: str
    span: Span
    macro: str | None = None
    call_site: Span | None = None
    def_span: Span | None = None


@dataclass(frozen=True)
class PToken:
    text: str
    kind: str
    frames: tuple = ()


@dataclass(frozen=True)
class MacroDef:
    name: str
    params: tuple
    body: tuple          # lexer Token，字节偏移基于宏定义文本
    def_span: Span
    doc: str
    text: str

    @staticmethod
    def parse(text: str) -> "MacroDef":
        toks = lex(text)

        def at():
            return toks[pos][0] if pos < len(toks) else None

        pos = 0
        if pos >= len(toks) or toks[pos].kind != "IDENT":
            raise MacroError(f"宏定义必须以名称开头: {text!r}")
        name = toks[pos].text
        pos += 1
        if pos >= len(toks) or toks[pos].text != "(":
            raise MacroError(f"宏定义缺少 '(': {text!r}")
        pos += 1
        params = []
        if pos < len(toks) and toks[pos].text != ")":
            while True:
                if pos >= len(toks) or toks[pos].kind != "IDENT":
                    raise MacroError(f"宏参数必须是标识符: {text!r}")
                params.append(toks[pos].text)
                pos += 1
                if pos >= len(toks):
                    raise MacroError(f"宏定义参数表不完整: {text!r}")
                if toks[pos].text == ",":
                    pos += 1
                    continue
                break
        if pos >= len(toks) or toks[pos].text != ")":
            raise MacroError(f"宏定义缺少 ')': {text!r}")
        pos += 1
        if pos >= len(toks) or toks[pos].text != "=":
            raise MacroError(f"宏定义缺少 '=': {text!r}")
        pos += 1
        body = tuple(toks[pos:])
        if not body:
            raise MacroError(f"宏体不能为空: {text!r}")
        doc = f"macro:{name}"
        def_span = Span(doc, body[0].start, body[-1].end)
        return MacroDef(name, tuple(params), body, def_span, doc, text)


def _head_span(tok: PToken) -> Span | None:
    return tok.frames[0].span if tok.frames else None


def parse_call_args(tokens, lparen):
    """tokens[lparen] == '('，返回 (实参 token 表列表, 右括号后下标)。"""
    args = []
    depth = 0
    cur = []
    i = lparen
    while i < len(tokens):
        t = tokens[i]
        if t.text == "(":
            depth += 1
            if depth > 1:
                cur.append(t)
        elif t.text == ")":
            depth -= 1
            if depth == 0:
                args.append(cur)
                return args, i + 1
            cur.append(t)
        elif t.text == "," and depth == 1:
            args.append(cur)
            cur = []
        else:
            cur.append(t)
        i += 1
    raise MacroError("宏调用括号不配对")


def expand_tokens(tokens, macros, active=frozenset()):
    out = []
    i = 0
    while i < len(tokens):
        t = tokens[i]
        if (
            t.kind == "IDENT"
            and t.text in macros
            and t.text not in active
            and i + 1 < len(tokens)
            and tokens[i + 1].text == "("
        ):
            name = t.text
            mdef = macros[name]
            args, j = parse_call_args(tokens, i + 1)
            if len(args) == 1 and not args[0]:
                args = []
            if len(args) != len(mdef.params):
                raise MacroError(
                    f"宏 {name} 需要 {len(mdef.params)} 个实参，实际 {len(args)} 个"
                )

            first = _head_span(tokens[i])
            last = _head_span(tokens[j - 1])
            if first is not None and last is not None and first.doc == last.doc:
                call_site = Span(first.doc, first.start, last.end)
            else:
                call_site = first

            # 先展开实参，再给每个实参 token 追加本次调用的上下文帧
            exp_args = []
            for arg in args:
                expanded_arg = expand_tokens(arg, macros, active)
                if call_site is not None:
                    ctx = Frame(
                        "call_context", call_site,
                        macro=name, def_span=mdef.def_span,
                    )
                    expanded_arg = [
                        PToken(p.text, p.kind, p.frames + (ctx,)) for p in expanded_arg
                    ]
                exp_args.append(expanded_arg)

            # 参数替换；非参数 token 记为宏体生成片段
            body = []
            for bt in mdef.body:
                if bt.text in mdef.params:
                    body.extend(exp_args[mdef.params.index(bt.text)])
                else:
                    frame = Frame(
                        "macro_body", Span(mdef.doc, bt.start, bt.end),
                        macro=name, call_site=call_site, def_span=mdef.def_span,
                    )
                    body.append(PToken(bt.text, bt.kind, (frame,)))

            # 继续展开宏体中的嵌套宏调用；active 防止递归
            out.extend(expand_tokens(body, macros, active | {name}))
            i = j
        else:
            out.append(t)
            i += 1
    return out


def render(tokens):
    """把展开后的 token 流渲染为文本，返回 (文本, 各 token 的字节区间)。"""
    parts = []
    spans = []
    pos = 0
    for t in tokens:
        parts.append(t.text)
        size = len(t.text.encode("utf-8"))
        spans.append(Span("expanded", pos, pos + size))
        pos += size + 1
    return " ".join(parts), spans
