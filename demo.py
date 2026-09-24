"""运行样例：python3.11 demo.py"""
import math

from dedup import DedupEngine

SCHEMA = [("id", "int"), ("name", "str"), ("score", "float")]


def show(title, entries):
    print(f"== {title} ==")
    for seq, row in entries:
        print(f"  seq={seq}  row={row}")
    print()


# 1) 保留首条：跨两个批次，null 键按相等去重
eng = DedupEngine(["name"], keep="first")
eng.add_batch(SCHEMA, [(1, "alice", 90.0), (2, None, 80.0), (3, "alice", 91.0)])
eng.add_batch(SCHEMA, [(4, None, 70.0), (5, "bob", 60.0)])
show("keep=first, key=[name], 两个批次（null 键相等）", eng.result_entries())

# 2) 保留末条：批次更新旧 ID，按声明事件序生效；结果顺序取保留行原序号
eng = DedupEngine(["id"], keep="last")
eng.add_batch(SCHEMA, [(1, "alice", 90.0), (2, "bob", 80.0)])
eng.add_batch(SCHEMA, [(1, "alice", 95.5)])  # 更新旧 id=1
show("keep=last, key=[id], 批次更新旧 ID", eng.result_entries())

# 3) 强制哈希碰撞：所有键哈希相同，仍须区分值
eng = DedupEngine(["id"], keep="first", hash_fn=lambda key: 0)
eng.add_batch(SCHEMA, [(1, "a", 1.0), (2, "b", 2.0), (1, "a2", 3.0), (3, "c", 3.0)])
show("keep=first, key=[id], 强制哈希碰撞", eng.result_entries())

# 4) null 与 NaN 不同；整数精确计算
eng = DedupEngine(["score"], keep="first")
eng.add_batch(SCHEMA, [(1, "null", None), (2, "nan", math.nan), (3, "null2", None)])
show("keep=first, key=[score], null != NaN", eng.result_entries())

big = 2 ** 63
eng = DedupEngine(["id"], keep="first")
eng.add_batch(SCHEMA, [(big, "x", 1.0), (big + 1, "y", 2.0), (big, "x2", 3.0)])
show("keep=first, key=[id], 大整数精确区分 2^63 与 2^63+1", eng.result_entries())

# 5) 空键列：全表同一组
eng = DedupEngine([], keep="last")
eng.add_batch(SCHEMA, [(1, "a", 1.0), (2, "b", 2.0)])
eng.add_batch(SCHEMA, [(3, "c", 3.0)])
show("keep=last, key=[]（空键列，全表一组）", eng.result_entries())
