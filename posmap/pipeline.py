"""Versioned pipeline: source -> macro expansion -> bytecode, with
interval-relation maps at every layer, local recompilation after macro
updates, and diagnostics that reverse-map bytecode pcs to user fragments.

Key properties:

* Every version keeps its own source text, expanded buffer, bytecode and
  maps.  A diagnostic is ``(version, pc)``; coordinates are never applied
  across versions.
* ``update_macro`` re-expands and re-compiles only the statements whose
  expansion (transitively) used the changed macro, and reports how many
  mapping blocks were touched.
* Reverse lookup returns *all* candidates, each with a reason, plus the
  chosen "most appropriate user fragment" and why it was chosen.
"""
from __future__ import annotations

from dataclasses import dataclass, replace

from . import lang
from .intervals import Block, Interval, IntervalMap


# ------------------------------------------------------------------ model

@dataclass(frozen=True)
class SrcRef:
    """A source interval stored relative to a stable anchor.

    Anchors let unaffected compilation units survive a local recompile
    unchanged: ``stmt`` is relative to the unit's own statement span,
    ``def`` is relative to the named macro's definition statement span.
    Absolute intervals are only resolved when a version is assembled.
    """

    kind: str               # 'stmt' | 'def'
    macro: str | None       # macro name for kind == 'def'
    rel: Interval


@dataclass(frozen=True)
class RelBlock:
    src: SrcRef | None      # None => generated fragment
    dst: Interval           # relative to the unit's expanded segment
    tag: str
    meta: tuple = ()        # (key, str | SrcRef) pairs


@dataclass
class StmtUnit:
    """One independently expandable/compilable top-level statement."""

    index: int
    kind: str               # 'let' | 'print' | 'expr' | 'macrodef'
    src_span: Interval      # absolute in the unit's version
    expanded: str           # expanded segment text ('' for macrodef)
    map1_rel: tuple         # RelBlocks: source -> expanded segment
    instrs: tuple           # Instrs with segment-relative spans
    used_macros: frozenset  # macros (transitively) used by the expansion


@dataclass
class Version:
    vid: int
    source: str
    units: list
    macros: dict
    expanded: str
    instrs: list
    map1: IntervalMap       # source bytes -> expanded bytes
    map2: IntervalMap       # expanded bytes -> bytecode pcs
    composed: IntervalMap   # source bytes -> bytecode pcs


@dataclass
class UpdateReport:
    macro: str
    old_vid: int
    new_vid: int
    recompiled_units: list  # statement indices that were re-expanded/re-compiled
    touched_blocks: int     # map blocks removed + added by the local update


@dataclass
class Diagnostic:
    vid: int
    pc: int
    message: str


@dataclass
class Candidate:
    tag: str                # 'source' | 'macro_body' | 'generated'
    user_span: Interval | None
    line: int | None        # None for generated code: never fabricated
    snippet: str | None
    macro: str | None
    def_span: Interval | None
    reason: str


@dataclass
class Diagnosis:
    diagnostic: Diagnostic
    candidates: list        # every candidate, each with its reason
    best_span: Interval | None
    best_line: int | None
    best_reason: str


# macro_body first: for macro-originated instructions the call range is the
# most appropriate user fragment; plain user code next; generated last.
RANK = {"macro_body": 0, "source": 1, "generated": 2}


def _line_of(source: str, span: Interval) -> int:
    """1-based line number of a *real* source span (byte based)."""

    return source.encode("utf-8")[: span.start].count(b"\n") + 1


def _snippet(source: str, span: Interval) -> str:
    return source.encode("utf-8")[span.start : span.end].decode("utf-8")


# ------------------------------------------------------------------ pipeline

class Pipeline:
    def __init__(self, source: str):
        self.versions: dict[int, Version] = {}
        self.current = 0
        stmts = self._parse(source)
        macros = self._macros(stmts)
        units = [self._build_unit(i, s, macros) for i, s in enumerate(stmts)]
        self.versions[0] = self._assemble(0, source, units, macros)

    # ---- construction helpers ----

    @staticmethod
    def _parse(source: str):
        return lang.parse_program(lang.lex(source))

    @staticmethod
    def _macros(stmts) -> dict:
        return {s.name: s for s in stmts if isinstance(s, lang.MacroDef)}

    def _build_unit(self, index: int, stmt, macros: dict) -> StmtUnit:
        if isinstance(stmt, lang.MacroDef):
            return StmtUnit(index, "macrodef", stmt.span, "", (), (), frozenset())
        used: set[str] = set()
        etoks = lang.expand_stmt(stmt, macros, used)
        toks: list[lang.Token] = []
        parts: list[str] = []
        off = 0
        for et in etoks:
            width = len(et.text.encode("utf-8"))
            toks.append(lang.Token(et.kind, et.text, Interval(off, off + width)))
            parts.append(et.text)
            off += width + 1
        segment = " ".join(parts)
        rel_blocks = tuple(
            self._rel_block(et.prov, tk.span, stmt, macros)
            for et, tk in zip(etoks, toks)
        )
        asts = lang.parse_program(toks)
        if len(asts) != 1:
            raise lang.ParseError("expanded segment is not a single statement")
        instrs: list[lang.Instr] = []
        lang.compile_stmt(asts[0], instrs)
        if isinstance(stmt, lang.Let):
            kind = "let"
        elif isinstance(stmt, lang.Print):
            kind = "print"
        else:
            kind = "expr"
        return StmtUnit(
            index, kind, stmt.span, segment, rel_blocks, tuple(instrs), frozenset(used)
        )

    @staticmethod
    def _rel_block(prov: lang.Prov, dst: Interval, stmt, macros: dict) -> RelBlock:
        if prov.kind == "source":
            base = stmt.span.start
            src = SrcRef("stmt", None, Interval(prov.src_span.start - base,
                                                prov.src_span.end - base))
            return RelBlock(src, dst, "source")
        if prov.kind == "macro_body":
            def_base = macros[prov.macro_name].span.start
            src = SrcRef("def", prov.macro_name,
                         Interval(prov.src_span.start - def_base,
                                  prov.src_span.end - def_base))
            stmt_base = stmt.span.start
            meta = (
                ("macro", prov.macro_name),
                ("call", SrcRef("stmt", None,
                                Interval(prov.call_span.start - stmt_base,
                                         prov.call_span.end - stmt_base))),
                ("def", SrcRef("def", prov.macro_name,
                               Interval(prov.def_span.start - def_base,
                                        prov.def_span.end - def_base))),
            )
            return RelBlock(src, dst, "macro_body", meta)
        return RelBlock(None, dst, "generated")

    @staticmethod
    def _resolve(ref: SrcRef, unit: StmtUnit, macros: dict) -> Interval:
        base = unit.src_span.start if ref.kind == "stmt" else macros[ref.macro].span.start
        return ref.rel.shift(base)

    def _assemble(self, vid: int, source: str, units: list, macros: dict) -> Version:
        expanded = ""
        map1_blocks: list[Block] = []
        map2_blocks: list[Block] = []
        instrs: list[lang.Instr] = []
        pc = 0
        for unit in units:
            if unit.kind == "macrodef":
                continue
            seg_off = len(expanded.encode("utf-8"))
            if expanded:
                expanded += "\n"
                seg_off += 1
            expanded += unit.expanded
            for rb in unit.map1_rel:
                src = None if rb.src is None else self._resolve(rb.src, unit, macros)
                meta = tuple(
                    (k, v if isinstance(v, str) else self._resolve(v, unit, macros))
                    for k, v in rb.meta
                )
                map1_blocks.append(Block(src, rb.dst.shift(seg_off), rb.tag, meta))
            for ins in unit.instrs:
                dst = Interval(pc, pc + 1)
                if ins.span is None:
                    map2_blocks.append(Block(None, dst, "generated"))
                else:
                    map2_blocks.append(Block(ins.span.shift(seg_off), dst, "code"))
                instrs.append(ins)
                pc += 1
        instrs.append(lang.Instr("HALT"))
        map2_blocks.append(Block(None, Interval(pc, pc + 1), "generated"))
        map1 = IntervalMap(map1_blocks, "source->expansion")
        map2 = IntervalMap(map2_blocks, "expansion->bytecode")
        return Version(
            vid, source, units, macros, expanded, instrs, map1, map2,
            map1.compose(map2, "source->bytecode"),
        )

    # ---- local recompilation ----

    def update_macro(self, name: str, new_body: str) -> UpdateReport:
        """Replace one macro's body and locally recompile what used it.

        Only statements whose expansion (transitively) referenced ``name``
        are re-expanded and re-compiled; every other unit is reused as-is.
        The previous version is kept intact for old diagnostics.
        """

        old = self.versions[self.current]
        if name not in old.macros:
            raise KeyError(f"no macro named {name!r}")
        body_span = old.macros[name].body.span
        data = old.source.encode("utf-8")
        new_source = (
            data[: body_span.start] + new_body.encode("utf-8") + data[body_span.end:]
        ).decode("utf-8")
        stmts = self._parse(new_source)
        macros = self._macros(stmts)
        if len(stmts) != len(old.units):
            raise lang.ParseError("macro body update changed the statement count")
        units: list[StmtUnit] = []
        recompiled: list[int] = []
        touched = 0
        for i, stmt in enumerate(stmts):
            prev = old.units[i]
            if isinstance(stmt, lang.MacroDef):
                units.append(self._build_unit(i, stmt, macros))
            elif name not in prev.used_macros:
                # unaffected: reuse the compiled unit, only re-anchor its span
                units.append(replace(prev, index=i, src_span=stmt.span))
            else:
                fresh = self._build_unit(i, stmt, macros)
                touched += len(fresh.map1_rel) + len(fresh.instrs)
                touched += len(prev.map1_rel) + len(prev.instrs)
                recompiled.append(i)
                units.append(fresh)
        ver = self._assemble(old.vid + 1, new_source, units, macros)
        self.versions[ver.vid] = ver
        self.current = ver.vid
        return UpdateReport(name, old.vid, ver.vid, recompiled, touched)

    # ---- execution & diagnostics ----

    def run(self, vid: int | None = None):
        """Run a version; returns ``(output, Diagnostic | None)``."""

        ver = self.versions[self.current if vid is None else vid]
        try:
            return lang.run_program(ver.instrs), None
        except lang.VMError as exc:
            return [], Diagnostic(ver.vid, exc.pc, exc.message)

    def explain(self, diagnostic: Diagnostic) -> Diagnosis:
        return self.explain_raw(diagnostic.vid, diagnostic.pc, diagnostic.message)

    def explain_raw(self, vid: int, pc: int, message: str = "") -> Diagnosis:
        ver = self.versions[vid]
        if not 0 <= pc < len(ver.instrs):
            raise ValueError(
                f"pc {pc} is not a valid coordinate for version {vid} "
                f"({len(ver.instrs)} instructions); bytecode coordinates are "
                "never applied across versions"
            )
        blocks = ver.composed.backward(Interval(pc, pc + 1))
        blocks.sort(
            key=lambda b: (
                RANK[b.tag],
                b.src.start if b.src else -1,
                b.src.end if b.src else -1,
            )
        )
        cands = [self._candidate(ver, b) for b in blocks]
        diag = Diagnostic(vid, pc, message)
        if not cands:
            return Diagnosis(diag, [], None, None, "no mapping block covers this instruction")
        top_rank = RANK[cands[0].tag]
        top = [c for c in cands if RANK[c.tag] == top_rank]
        spans = [c.user_span for c in top if c.user_span is not None]
        if not spans:
            return Diagnosis(
                diag, cands, None, None,
                "no user source for this instruction; it is generated code, "
                "so no line number is fabricated",
            )
        best = Interval(min(s.start for s in spans), max(s.end for s in spans))
        if cands[0].tag == "macro_body":
            reason = (
                "instruction originates from a macro body; the call range is "
                "the most appropriate user fragment (definition attached)"
            )
        else:
            reason = "instruction maps directly to user-written source"
        return Diagnosis(diag, cands, best, _line_of(ver.source, best), reason)

    @staticmethod
    def _candidate(ver: Version, block: Block) -> Candidate:
        if block.src is None or block.tag == "generated":
            return Candidate(
                "generated", None, None, None, None, None,
                "generated fragment with no user source; no line number fabricated",
            )
        if block.tag == "macro_body":
            meta = dict(block.meta)
            call = meta.get("call")
            defspan = meta.get("def")
            mname = meta.get("macro")
            return Candidate(
                "macro_body", call, _line_of(ver.source, call),
                _snippet(ver.source, call), mname, defspan,
                f"from macro '{mname}' body; user location is the call range, "
                "definition attached",
            )
        return Candidate(
            "source", block.src, _line_of(ver.source, block.src),
            _snippet(ver.source, block.src), None, None,
            "direct user-written code",
        )


# ------------------------------------------------------------------ consistency helpers

def chained_source_intervals(ver: Version, pc: int) -> set:
    """Per-layer reverse query: bytecode -> expanded -> source."""

    out = set()
    for b2 in ver.map2.backward(Interval(pc, pc + 1)):
        if b2.src is None:
            out.add(None)
            continue
        hits = ver.map1.backward(b2.src)
        if not hits:
            out.add(None)
        out.update(b1.src for b1 in hits)
    return out


def composed_source_intervals(ver: Version, pc: int) -> set:
    """One-shot reverse query through the composed map."""

    return {b.src for b in ver.composed.backward(Interval(pc, pc + 1))}
