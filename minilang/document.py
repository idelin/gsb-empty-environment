"""增量解析入口:Document 维护文本与语法树,按编辑范围复用未变子树。"""

from .nodes import compute_positions
from .parser import parse_text


class Document:
    def __init__(self, text):
        self.text = text
        self.tree, self.tokens, self.errors = parse_text(self.text)
        self.positions = compute_positions(self.tree)

    def apply_edit(self, start, end, replacement):
        """应用一次编辑:[start, end) 为旧文本的 UTF-8 字节区间,
        replacement 为替换进去的新文本。编辑后未受影响的顶层声明
        节点对象被原样复用(可用 `is` 判定),位置随编辑自动更新。
        """
        old_bytes = self.text.encode("utf-8")
        if not (0 <= start <= end <= len(old_bytes)):
            raise ValueError("编辑区间越界")
        new_bytes = old_bytes[:start] + replacement.encode("utf-8") + old_bytes[end:]
        try:
            new_text = new_bytes.decode("utf-8")
        except UnicodeDecodeError as exc:
            raise ValueError("编辑边界必须落在 UTF-8 字符边界上") from exc

        delta = len(replacement.encode("utf-8")) - (end - start)
        reusable = {}
        for decl in self.tree.decls:
            decl_start, decl_end = self.positions[id(decl)]
            if decl_end <= start:
                new_start = decl_start            # 编辑点之前,位置不变
            elif decl_start >= end:
                new_start = decl_start + delta    # 编辑点之后,整体平移
            else:
                continue                          # 与编辑相交,必须重解析
            count = sum(
                1
                for tok in self.tokens
                if tok.kind != "EOF" and decl_start <= tok.start < decl_end
            )
            reusable[new_start] = (decl, count)

        self.text = new_text
        self.tree, self.tokens, self.errors = parse_text(self.text, reusable)
        self.positions = compute_positions(self.tree)
