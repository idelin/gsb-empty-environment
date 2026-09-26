"""本地合成的 UTF-8 文本语料（不依赖任何外部数据或搜索服务）。"""

from __future__ import annotations

import random


def synth_documents(seed: int = 20260926) -> list[tuple[str, str]]:
    """生成 12 篇确定性的中英混合 UTF-8 文档，返回 (doc_id, text)。"""
    rnd = random.Random(seed)
    english = [
        ("doc-en-01",
         "Background merge compacts immutable segments without blocking readers."),
        ("doc-en-02",
         "A deletion bitmap marks live documents inside each immutable segment."),
        ("doc-en-03",
         "Readers keep a snapshot, so background merge never disturbs old queries."),
        ("doc-en-04",
         "The new version of a document shadows every older version."),
        ("doc-en-05",
         "Statistics count only visible documents; tombstones are never counted."),
        ("doc-en-06",
         "Inverted postings map a term to the documents that contain it."),
    ]
    chinese = [
        ("doc-zh-01", "后台合并压缩不可变段，同时保持读快照不受影响。"),
        ("doc-zh-02", "删除位图记录每个不可变段中仍然可见的文档。"),
        ("doc-zh-03", "新版本写入新段，旧版本被遮蔽，读者仍可查询旧段。"),
        ("doc-zh-04", "墓碑没有覆盖所有更旧段之前，不能丢弃。"),
        ("doc-zh-05", "统计只计算可见文档，墓碑不计入任何统计。"),
        ("doc-zh-06", "合并期间更新文档不会丢失新值，合并结果会被重新遮蔽。"),
    ]
    docs = english + chinese
    rnd.shuffle(docs)  # 写入顺序打乱，但 doc_id/内容固定
    return docs
