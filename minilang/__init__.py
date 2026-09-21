"""minilang: a tiny command language with CFG-based dataflow analysis.

Pipeline: lex -> parse -> build CFG -> analyze (reaching definitions,
definitely-assigned, use-before-def, unreachable code) -> interpret.
All source positions are UTF-8 byte offsets [start, end).
"""
