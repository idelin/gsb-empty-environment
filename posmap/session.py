"""编译会话：原文 -> 宏展开 -> 字节码 的多层映射、诊断解释与局部重编译。

两条映射：
  M1: expanded -> origin(program / macro:NAME)，按展开 token 建立
  M2: code(pc) -> expanded，按指令建立
  组合映射 composed = M2 ∘ M1: code -> origin

每个版本（初始编译 + 每次宏更新）保存不可变快照；旧诊断记录产生它的
版本号，解释时永远查该版本快照，不把新字节码坐标套到旧映射上。
"""
from __future__ import annotations

from dataclasses import dataclass

from .compiler import Instr, compile_statement
from .intervals import Block, IntervalMap, Span
from .lexer import lex
from .macros import Frame, MacroDef, MacroError, PToken, expand_tokens, render
from .parser import parse_statement
from .vm import RuntimeFault, execute


@dataclass(frozen=True)
class Diagnostic:
    version: int
    pc: int
    op: str
    message: str


@dataclass(frozen=True)
class Candidate:
    role: str          # user / call_site / macro_body / definition
    span: Span
    reason: str
    macro: str | None = None


@dataclass
class Explanation:
    version: int
    pc: int
    op: str
    message: str | None
    candidates: list
    best: Candidate | None
    best_reason: str


@dataclass
class UpdateReport:
    macro: str
    changed: bool
    old_version: int
    new_version: int
    recompiled_statements: list
    touched_blocks: int    # 受影响语句块中重新计算的 M1+M2 映射块数
    shifted_blocks: int    # 复用块中因前面的块长度变化而平移坐标的块数
    reused_blocks: int     # 完全复用、坐标未变的块数


@dataclass
class _Chunk:
    text: str
    m1: list       # Block：局部 expanded -> origin
    instrs: list
    m2: list       # Block：局部 pc -> 局部 expanded


@dataclass
class _Snapshot:
    version: int
    program: str
    expanded_text: str
    macros: dict
    instrs: list
    m1: IntervalMap
    m2: IntervalMap
    composed: IntervalMap


_ROLE_RANK = {"user": 0, "call_site": 1, "macro_body": 2, "definition": 3}


def _candidates_from_frames(frames):
    cands = []
    for fr in frames:
        if fr.kind == "user":
            cands.append(Candidate("user", fr.span, "直接来自用户原文的片段"))
        elif fr.kind == "macro_body":
            cands.append(Candidate(
                "macro_body", fr.span,
                f"宏 {fr.macro} 展开体中的生成片段", fr.macro))
            if fr.call_site is not None:
                cands.append(Candidate(
                    "call_site", fr.call_site,
                    f"宏 {fr.macro} 的调用处（用户代码，生成片段的最合适回溯）",
                    fr.macro))
            if fr.def_span is not None:
                cands.append(Candidate(
                    "definition", fr.def_span,
                    f"宏 {fr.macro} 的定义位置", fr.macro))
        elif fr.kind == "call_context":
            cands.append(Candidate(
                "call_site", fr.span,
                f"外层宏 {fr.macro} 的调用处（用户代码）", fr.macro))
            if fr.def_span is not None:
                cands.append(Candidate(
                    "definition", fr.def_span,
                    f"外层宏 {fr.macro} 的定义位置", fr.macro))
    return cands


def _select_best(candidates):
    if not candidates:
        return None, "该指令没有任何来源映射（如生成的 HALT），不伪造行号"
    best = min(candidates, key=lambda c: _ROLE_RANK[c.role])
    if best.role == "user":
        reason = "存在用户直接编写的片段，优先级最高"
    elif best.role == "call_site":
        reason = "指令来自宏展开生成的片段，回溯到最合适的用户片段——宏调用处"
    elif best.role == "macro_body":
        reason = "只能定位到宏定义体内的生成片段，没有更直接的用户片段"
    else:
        reason = "只有宏定义位置可用"
    return best, reason


def _split_statements(tokens):
    stmts = []
    depth = 0
    cur = []
    for t in tokens:
        cur.append(t)
        if t.text == "(":
            depth += 1
        elif t.text == ")":
            depth -= 1
        elif t.text == ";" and depth == 0:
            stmts.append(cur)
            cur = []
    if cur:
        stmts.append(cur)  # 缺少分号，交给 parser 报错
    return stmts


class Session:
    def __init__(self, program: str, macros: dict):
        self.program = program
        self.macro_texts = dict(macros)
        self.macros = {}
        for key, text in macros.items():
            mdef = MacroDef.parse(text)
            if mdef.name != key:
                raise MacroError(f"宏名不一致: {key} vs {mdef.name}")
            self.macros[key] = mdef
        self._stmt_tokens = _split_statements(lex(program))
        self._chunks = [self._compile_chunk(t) for t in self._stmt_tokens]
        self.version = 0
        self._bases = []
        self._snapshots = {}
        self._link()
        self._store_snapshot()

    @property
    def expanded_text(self):
        return self._expanded_text

    @property
    def instructions(self):
        return self._instrs

    def _compile_chunk(self, stmt_tokens) -> _Chunk:
        ptoks = [
            PToken(t.text, t.kind, (Frame("user", Span("program", t.start, t.end)),))
            for t in stmt_tokens
        ]
        expanded = expand_tokens(ptoks, self.macros)
        text, spans = render(expanded)
        etoks = lex(text)
        if [t.text for t in etoks] != [p.text for p in expanded]:
            raise RuntimeError("展开文本重新词法分析结果不一致")
        stmt = parse_statement(etoks)
        instrs = compile_statement(stmt)
        m1 = [
            Block(src, p.frames[0].span, p.frames)
            for p, src in zip(expanded, spans)
        ]
        m2 = [
            Block(Span("code", i, i + 1), ins.span)
            for i, ins in enumerate(instrs)
            if ins.span is not None
        ]
        return _Chunk(text, m1, instrs, m2)

    def _link(self):
        m1 = IntervalMap("expanded", "origin")
        m2 = IntervalMap("code", "expanded")
        instrs = []
        bases = []
        texts = []
        eb = 0
        pb = 0
        for chunk in self._chunks:
            texts.append(chunk.text)
            bases.append((eb, pb))
            for b in chunk.m1:
                m1.add(b.src.shift(eb), b.dst, b.tag)
            for b in chunk.m2:
                m2.add(b.src.shift(pb), b.dst.shift(eb), b.tag)
            for ins in chunk.instrs:
                span = ins.span.shift(eb) if ins.span is not None else None
                instrs.append(Instr(ins.op, ins.arg, span))
            eb += len(chunk.text.encode("utf-8")) + 1
            pb += len(chunk.instrs)
        instrs.append(Instr("HALT"))  # 生成指令：无来源
        self._expanded_text = "\n".join(texts)
        self._instrs = instrs
        self._m1 = m1
        self._m2 = m2
        self._composed = m2.compose(m1)
        self._bases = bases

    def _store_snapshot(self):
        self._snapshots[self.version] = _Snapshot(
            self.version, self.program, self._expanded_text,
            dict(self.macros), list(self._instrs),
            self._m1, self._m2, self._composed,
        )

    def _compute_deps(self):
        mrefs = {
            name: {
                t.text for t in mdef.body
                if t.kind == "IDENT" and t.text in self.macros
            }
            for name, mdef in self.macros.items()
        }

        def closure(seed):
            seen = set(seed)
            stack = list(seed)
            while stack:
                cur = stack.pop()
                for ref in mrefs.get(cur, ()):
                    if ref not in seen:
                        seen.add(ref)
                        stack.append(ref)
            return seen

        deps = []
        for toks in self._stmt_tokens:
            direct = {
                t.text for t in toks
                if t.kind == "IDENT" and t.text in self.macros
            }
            deps.append(closure(direct))
        return deps

    def update_macro(self, name: str, text: str) -> UpdateReport:
        if self.macro_texts.get(name) == text:
            return UpdateReport(
                name, False, self.version, self.version, [], 0, 0, 0)
        new_def = MacroDef.parse(text)
        if new_def.name != name:
            raise MacroError(f"宏名不一致: {name} vs {new_def.name}")

        old_version = self.version
        old_bases = list(self._bases)
        old_macros = self.macros
        old_texts = self.macro_texts
        new_macros = dict(old_macros)
        new_macros[name] = new_def
        new_texts = dict(old_texts)
        new_texts[name] = text
        self.macros = new_macros
        self.macro_texts = new_texts
        try:
            affected = [
                i for i, deps in enumerate(self._compute_deps())
                if name in deps
            ]
            rebuilt = {}
            touched = 0
            for i in affected:
                chunk = self._compile_chunk(self._stmt_tokens[i])
                rebuilt[i] = chunk
                touched += len(chunk.m1) + len(chunk.m2)
        except Exception:
            self.macros = old_macros
            self.macro_texts = old_texts
            raise
        for i, chunk in rebuilt.items():
            self._chunks[i] = chunk
        self._link()

        shifted = 0
        reused = 0
        affected_set = set(affected)
        for i, chunk in enumerate(self._chunks):
            if i in affected_set:
                continue
            blocks = len(chunk.m1) + len(chunk.m2)
            if i < len(old_bases) and old_bases[i] != self._bases[i]:
                shifted += blocks
            else:
                reused += blocks

        self.version += 1
        self._store_snapshot()
        return UpdateReport(
            name, True, old_version, self.version, affected,
            touched, shifted, reused)

    def run(self):
        snap = self._snapshots[self.version]
        output = []
        try:
            execute(snap.instrs, output)
        except RuntimeFault as fault:
            op = snap.instrs[fault.pc].op
            return output, Diagnostic(
                self.version, fault.pc, op, fault.message)
        return output, None

    def _snapshot(self, version):
        try:
            return self._snapshots[version]
        except KeyError:
            raise KeyError(
                f"版本 v{version} 的快照不存在，拒绝回查"
                f"（不能把新字节码坐标套到旧源文）")

    def explain(self, diag: Diagnostic) -> Explanation:
        return self._explain_at(diag.pc, diag.version, diag.message)

    def explain_pc(self, pc: int, version: int | None = None) -> Explanation:
        ver = self.version if version is None else version
        return self._explain_at(pc, ver, None)

    def _explain_at(self, pc, version, message):
        snap = self._snapshot(version)
        if not (0 <= pc < len(snap.instrs)):
            raise ValueError(f"pc 越界: {pc}")
        ins = snap.instrs[pc]
        hits = snap.composed.targets(Span("code", pc, pc + 1))
        candidates = []
        seen = set()
        for h in hits:
            for cand in _candidates_from_frames(h.tag):
                key = (cand.role, cand.span)
                if key not in seen:
                    seen.add(key)
                    candidates.append(cand)
        best, reason = _select_best(candidates)
        return Explanation(
            version, pc, ins.op, message, candidates, best, reason)

    def layered_lookup(self, pc: int, version: int | None = None):
        """逐层反查 code -> expanded -> origin，用于与组合映射交叉验证。"""
        ver = self.version if version is None else version
        snap = self._snapshot(ver)
        out = []
        for b2 in snap.m2.targets(Span("code", pc, pc + 1)):
            out.extend(snap.m1.targets(b2.dst))
        return out

    def _doc_text(self, snap: _Snapshot, doc: str):
        if doc == "program":
            return snap.program
        if doc == "expanded":
            return snap.expanded_text
        if doc.startswith("macro:"):
            return snap.macros[doc[len("macro:"):]].text
        return None

    @staticmethod
    def _line_info(text: str, span: Span):
        data = text.encode("utf-8")
        line_no = data[:span.start].count(b"\n") + 1
        line_start = data.rfind(b"\n", 0, span.start) + 1
        nl = data.find(b"\n", span.start)
        line_end = len(data) if nl == -1 else nl
        line_text = data[line_start:line_end].decode("utf-8")
        col = len(data[line_start:span.start].decode("utf-8"))
        width = max(
            1,
            len(data[span.start:min(span.end, line_end)].decode(
                "utf-8", "ignore")),
        )
        return line_no, col, line_text, width

    def format_span(self, version: int, span: Span) -> str:
        snap = self._snapshot(version)
        text = self._doc_text(snap, span.doc)
        if text is None:
            return f"{span.doc}[{span.start},{span.end})"
        line_no, col, line_text, _ = self._line_info(text, span)
        return f"{span.doc} 第{line_no}行第{col + 1}列: {line_text.strip()!r}"

    def format_explanation(self, ex: Explanation) -> str:
        lines = [
            f"诊断: {ex.message or '(ad-hoc 查询)'} "
            f"@ pc={ex.pc} op={ex.op} (版本 v{ex.version})"
        ]
        if ex.best is None:
            lines.append(f"最佳定位: 无 —— {ex.best_reason}")
        else:
            lines.append(
                f"最佳定位: {self.format_span(ex.version, ex.best.span)}")
            lines.append(f"选择原因: {ex.best_reason}")
            snap = self._snapshot(ex.version)
            text = self._doc_text(snap, ex.best.span.doc)
            if text is not None:
                _, col, line_text, width = self._line_info(text, ex.best.span)
                lines.append(f"    {line_text}")
                lines.append(f"    {' ' * col}{'^' * width}")
        if ex.candidates:
            lines.append(f"全部候选 ({len(ex.candidates)}):")
            for i, cand in enumerate(ex.candidates, 1):
                lines.append(
                    f"  {i}. [{cand.role}] "
                    f"{self.format_span(ex.version, cand.span)} —— {cand.reason}")
        return "\n".join(lines)
