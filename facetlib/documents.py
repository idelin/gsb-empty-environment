"""本地合成 UTF-8 文本文档：数据模型、生成、解析与读写。

文件格式（UTF-8 纯文本，每文档一个文件）::

    id: doc-0001
    @颜色: 红, 蓝, 红
    @作者: alice
    @年份:

    正文自由文本……（按 facetlib.tokenizer 的规则分词）

分面“缺失”与“空集合”的区分：
  * 完全没有 ``@<分面>`` 行       -> 该分面**缺失**（missing）；
  * 有 ``@<分面>:`` 但冒号后为空  -> 该分面**存在但为空集合**；
  * 同一值重复出现（如 ``红, 红``）-> 索引时按文档身份去重，计数不重复。
"""

from __future__ import annotations

import random
from dataclasses import dataclass, field
from pathlib import Path


@dataclass
class Doc:
    """一份文档。``facets`` 中**没有的键**表示该分面缺失；值为空元组表示空集合。"""

    doc_id: str
    text: str
    facets: dict[str, tuple[str, ...]] = field(default_factory=dict)


def render_doc(doc: Doc) -> str:
    lines = [f"id: {doc.doc_id}"]
    for facet in sorted(doc.facets):
        lines.append(f"@{facet}: {', '.join(doc.facets[facet])}")
    lines.append("")
    lines.append(doc.text)
    return "\n".join(lines) + "\n"


def parse_doc(text: str) -> Doc:
    doc_id = None
    facets: dict[str, tuple[str, ...]] = {}
    body: list[str] = []
    in_body = False
    for line in text.split("\n"):
        if not in_body:
            if line.startswith("id: "):
                doc_id = line[4:].strip()
            elif line.startswith("@"):
                name, _, rest = line[1:].partition(":")
                values = tuple(v.strip() for v in rest.split(",") if v.strip())
                facets[name.strip()] = values
            elif line.strip() == "":
                in_body = True
        else:
            body.append(line)
    if not doc_id:
        raise ValueError("文档缺少 id 行")
    return Doc(doc_id, "\n".join(body).strip(), facets)


def write_docs(directory: str | Path, docs: list[Doc]) -> None:
    directory = Path(directory)
    directory.mkdir(parents=True, exist_ok=True)
    for doc in docs:
        (directory / f"{doc.doc_id}.txt").write_text(render_doc(doc), encoding="utf-8")


def load_docs(directory: str | Path) -> list[Doc]:
    directory = Path(directory)
    return [parse_doc(p.read_text(encoding="utf-8")) for p in sorted(directory.glob("*.txt"))]


_COLORS = ("红", "绿", "蓝")
_AUTHORS = ("alice", "bob", "carol")
_YEARS = ("2023", "2024", "2025")
_SOURCES = ("web", "app")
_WORDS = ("苹果", "香蕉", "橘子", "手机", "电脑", "平板", "alpha", "beta")


def generate_docs(n: int = 240, seed: int = 7) -> list[Doc]:
    """确定性生成 n 份合成文档，覆盖缺失 / 空集合 / 重复标签等边界情况。"""
    rng = random.Random(seed)
    docs: list[Doc] = []
    for i in range(n):
        facets: dict[str, tuple[str, ...]] = {}
        r = rng.random()
        if r < 0.10:
            pass  # 颜色缺失
        elif r < 0.20:
            facets["颜色"] = ()  # 颜色存在但为空集合
        else:
            values = [rng.choice(_COLORS) for _ in range(rng.randint(1, 2))]
            if rng.random() < 0.25:
                values.append(values[0])  # 重复标签，索引时须去重
            facets["颜色"] = tuple(values)
        if rng.random() >= 0.10:
            facets["作者"] = (rng.choice(_AUTHORS),)
        if rng.random() >= 0.15:
            facets["年份"] = (rng.choice(_YEARS),)
        if rng.random() >= 0.10:
            facets["来源"] = (rng.choice(_SOURCES),)
        text = "，".join(rng.choice(_WORDS) for _ in range(rng.randint(3, 6))) + "。"
        docs.append(Doc(f"doc-{i:04d}", text, facets))
    return docs
