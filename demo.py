"""运行样例：本地合成 UTF-8 文档，演示多段 TopK 归并。

场景：三个索引段，文档含中文字段与 null 值；段 a 头部高分文档
均为英文（会被过滤），另有删除位图。按 (score 降序 NULLS LAST,
updated 升序 NULLS FIRST) 排序，过滤 lang == "zh" 且正文含
token "索引"，取 Top 5。
"""

from topk import Document, Field, Segment, SortField, merge_topk, tokenize


def doc(doc_id, **fields):
    return Document(doc_id=doc_id, fields=fields)


def build_segments():
    seg_a = Segment(
        "seg-a",
        [
            doc(101, lang="en", score=99, updated="2024-06-01", body="Merge sort topk"),
            doc(102, lang="en", score=98, updated="2024-06-02", body="Heap index"),
            doc(103, lang="en", score=97, updated="2024-06-03", body="Segment merge"),
            doc(104, lang="zh", score=96, updated="2024-06-04", body="分段索引的归并排序"),
            doc(105, lang="zh", score=95, updated=None, body="索引与删除位图"),
            doc(106, lang="zh", score=94, updated="2024-06-06", body="倒排索引简介"),
        ],
        deleted={106},  # 删除位图：106 不可见
    )
    seg_b = Segment(
        "seg-b",
        [
            doc(201, lang="zh", score=96, updated="2024-06-01", body="索引归并与过滤补足"),
            doc(202, lang="zh", score=None, updated="2024-06-02", body="索引为空分"),
            doc(203, lang="zh", score=93, updated="2024-06-03", body="ＴＯＰＫ索引归并"),
        ],
    )
    seg_c = Segment(
        "seg-c",
        [
            doc(301, lang="zh", score=96, updated=None, body="索引段头过滤演示"),
            doc(302, lang="en", score=95, updated="2024-06-05", body="Tokenizer rules"),
            doc(303, lang="zh", score=92, updated="2024-06-07", body="分词规则与索引"),
        ],
        deleted={303},
    )
    return [seg_a, seg_b, seg_c]


def main():
    print("== 分词示例 ==")
    print("ＴＯＰＫ索引归并 ->", tokenize("ＴＯＰＫ索引归并"))
    print()

    segments = build_segments()
    sort_spec = [
        SortField("score", desc=True, nulls_first=False),
        SortField("updated", desc=False, nulls_first=True),
    ]
    condition = (Field("lang") == "zh") & Field("body").contains_token("索引")

    print("== 排序规格 ==")
    for f in sort_spec:
        direction = "DESC" if f.desc else "ASC"
        nulls = "NULLS FIRST" if f.nulls_first else "NULLS LAST"
        print(f"  {f.name} {direction} {nulls}")
    print("  (同分按 doc_id 升序稳定)")
    print("过滤条件:", condition)
    print()

    print("== Top 5 结果 ==")
    for rank, d in enumerate(merge_topk(segments, sort_spec, k=5, filter=condition), 1):
        print(
            f"  #{rank} id={d.doc_id} score={d.get('score')} "
            f"updated={d.get('updated')} body={d.get('body')}"
        )
    print()
    print("说明：seg-a 头部 101/102/103 虽分数最高，但被过滤后直接跳过并")
    print("继续向后读取补足；106/303 命中删除位图，不参与归并。")


if __name__ == "__main__":
    main()
