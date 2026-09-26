"""本地合成 UTF-8 语料。

文档在源码中以字符串字面量声明（Python 3 源码默认 UTF-8），
不依赖任何外部数据源、搜索服务或网络。``build_demo_index`` 把 24 条文档
均匀固化为 3 个段（每段 8 条）。

字段：
- title: 可检索文本（中英文混合）；
- price: 价格，用于升降序；
- rating: 评分，部分文档为 None，用于演示 null 位置；
- promo: 是否促销（业务过滤器用）；
- flagship: 是否旗舰（业务过滤器用）；
- in_stock: 是否有货；
- updated: 更新序号（非空，附加排序字段）。
"""

from __future__ import annotations

from .document import Document
from .index import Index

_RAW: tuple[tuple[int, str, int, float | None, bool, bool, bool, int], ...] = (
    # 段 0
    (1, "wireless mouse 无线鼠标 ergonomic", 15, 4.2, False, False, True, 20),
    (2, "mechanical keyboard 机械键盘 青轴", 120, 4.7, True, False, True, 12),
    (3, "usb-c hub type-c 扩展坞", 100, 4.5, False, False, True, 18),
    (4, "4k monitor 显示器 27-inch 护眼", 320, 4.6, True, True, True, 30),
    (5, "laptop stand 笔记本支架 aluminum", 80, 4.0, False, False, True, 8),
    (6, "webcam 1080p 摄像头 with microphone", 60, 4.1, False, False, False, 15),
    (7, "desk mat 桌垫 mouse pad 大号", 18, None, True, False, True, 3),
    (8, "flagship laptop 旗舰笔记本 pro 16-inch", 1600, 4.9, False, True, True, 40),
    # 段 1
    (9, "wireless keyboard 无线键盘 静音", 90, 4.4, False, False, True, 22),
    (10, "mechanical keyboard 机械键盘 红轴 rgb", 150, 4.8, False, True, False, 25),
    (11, "portable ssd 移动固态硬盘 1tb", 85, 4.6, False, False, True, 19),
    (12, "office chair 人体工学办公椅 ergonomic", 250, 4.3, True, False, True, 11),
    (13, "flagship monitor 旗舰显示器 32-inch 4k", 450, 4.8, False, True, True, 35),
    (14, "cable usb-c 数据线 fast charge", 40, None, True, False, True, 2),
    (15, "noise cancelling headphones 降噪耳机", 200, 4.5, False, True, True, 28),
    (16, "flagship tablet 旗舰平板 12.9-inch", 900, 4.7, False, True, False, 16),
    # 段 2
    (17, "bluetooth speaker 蓝牙音箱 waterproof", 55, 4.0, True, False, True, 6),
    (18, "ergonomic mouse 人体工学鼠标 vertical", 70, 4.2, False, False, True, 17),
    (19, "mechanical keyboard 机械键盘 银轴 compact", 110, None, False, False, True, 9),
    (20, "ultrawide monitor 带鱼屏显示器 34-inch", 380, 4.4, False, True, True, 33),
    (21, "docking station 扩展坞 dual monitor", 140, 4.3, False, False, False, 24),
    (22, "flagship smartphone 旗舰手机 5g 256gb", 700, 4.6, False, True, True, 38),
    (23, "wireless earbuds 无线蓝牙耳机 降噪", 95, 4.5, True, False, True, 14),
    (24, "flagship camera 旗舰相机 full-frame 4k", 1500, 4.9, False, True, True, 41),
)

FIELDS = ("price", "rating", "promo", "flagship", "in_stock", "updated")


def make_documents() -> list[Document]:
    docs: list[Document] = []
    for doc_id, title, price, rating, promo, flagship, in_stock, updated in _RAW:
        docs.append(
            Document(
                doc_id=doc_id,
                text=title,
                fields={
                    "price": price,
                    "rating": rating,
                    "promo": promo,
                    "flagship": flagship,
                    "in_stock": in_stock,
                    "updated": updated,
                },
            )
        )
    return docs


def build_demo_index(deleted: tuple[int, ...] = ()) -> Index:
    """构建 3 段演示索引，并把给定 doc_id 标记进删除位图。"""
    docs = make_documents()
    index = Index()
    per_segment = len(docs) // 3
    for start in range(0, len(docs), per_segment):
        index.commit(docs[start : start + per_segment])
    for doc_id in deleted:
        index.delete(doc_id)
    return index
