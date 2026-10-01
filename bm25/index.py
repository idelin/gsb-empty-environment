"""可变 BM25 索引：增删文档并维护全局统计；支持不可变快照。"""

from collections import Counter

from .core import BM25Core
from .tokenizer import tokenize


class BM25Index(BM25Core):
    """支持 add/delete 的索引。doc_id 必须是 int（同分按 ID 升序、块按 ID 划分）。

    每次增删都会：
      * 更新 doc_len / df / N / total_len（avgdl 由它们派生）；
      * 将 version +1，使此前缓存的所有块上界立即失效（惰性重算）。
    """

    def __init__(self, k1=1.5, b=0.75, block_size=8):
        super().__init__(k1=k1, b=b, block_size=block_size)
        self._doc_tf = {}  # doc_id -> Counter(term->tf)，删除时用来精确回滚倒排

    def add_document(self, doc_id, text):
        if not isinstance(doc_id, int):
            raise TypeError("doc_id 必须是 int")
        if doc_id in self.doc_len:
            raise ValueError("doc_id 已存在: %r" % doc_id)
        tf = Counter(tokenize(text))
        self._doc_tf[doc_id] = tf
        self.doc_len[doc_id] = sum(tf.values())
        self.n_docs += 1
        self.total_len += self.doc_len[doc_id]
        for term, c in tf.items():
            self.postings.setdefault(term, {})[doc_id] = c
            self.df[term] = self.df.get(term, 0) + 1
        self.version += 1

    def delete_document(self, doc_id):
        tf = self._doc_tf.pop(doc_id, None)
        if tf is None:
            raise KeyError("doc_id 不存在: %r" % doc_id)
        self.n_docs -= 1
        self.total_len -= self.doc_len.pop(doc_id)
        for term, c in tf.items():
            posting = self.postings[term]
            del posting[doc_id]
            self.df[term] -= 1
            if not posting:
                del self.postings[term]
                del self.df[term]
        self.version += 1

    def snapshot(self):
        """固定当前统计与倒排的不可变快照（含当前版本号）。

        之后对本索引的任何增删都不影响快照；快照用自己的版本号校验
        自己的块上界缓存，因此快照内的上界永不失效。
        """
        snap = Snapshot(k1=self.k1, b=self.b, block_size=self.block_size)
        snap.postings = {t: dict(p) for t, p in self.postings.items()}
        snap.doc_len = dict(self.doc_len)
        snap.df = dict(self.df)
        snap.n_docs = self.n_docs
        snap.total_len = self.total_len
        snap.version = self.version
        return snap


class Snapshot(BM25Core):
    """不可变快照：统计与倒排在创建时固定，查询结果不随后续增删改变。"""
