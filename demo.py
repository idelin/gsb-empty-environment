"""运行样例：python3.11 demo.py

场景全覆盖：
1) 检索并产出解释树（匹配词/权重/统计版本/过滤理由/剪枝上界）；
2) 指定文档未命中原因查询；
3) 独立验证器复算命中并核验未命中理由；
4) 统计与规则更新后，旧解释保持历史有效；
5) 注入篡改：权重、遗漏过滤、替换文档版本 -> 定位首个不成立节点；
6) 缺统计证据 -> 只能判不可验证。
"""

import copy
import json

from ranker import DocStore, Engine, RulesStore, StatsStore, Verifier, explain_miss
from ranker.explanation import finalize

QUERY = {
    "phrases": [{"text": "Python", "weight": 2.0}, {"text": "机器学习", "weight": 1.0}],
    "filters": [{"field": "year", "op": ">=", "value": 2021}],
    "k": 3,
}


def build_world():
    docs = DocStore()
    docs.add("d1", "Python 机器学习 入门：用 Python 做 机器学习 实战 项目", {"year": 2023, "category": "tech"})
    docs.add("d2", "深度 学习 与 神经 网络 基础 教程", {"year": 2022, "category": "tech"})
    docs.add("d3", "Python 数据 分析 手册：pandas 与 numpy", {"year": 2021, "category": "tech"})
    docs.add("d4", "机器学习 理论 与 统计 学习 方法", {"year": 2019, "category": "book"})
    docs.add("d5", "Python Python Python 编程 编程", {"year": 2020, "category": "tech"})
    docs.add("d6", "今天 天气 不错 适合 出门 散步", {"year": 2023, "category": "life"})
    docs.add("d7", "机器学习 是 人工 智能 的 重要 分支", {"year": 2022, "category": "tech"})
    docs.add("d8", "机器 视觉 与 机器 翻译 的 学习 笔记", {"year": 2021, "category": "tech"})
    docs.add("d9", "Python 爬虫 入门 教程", {"year": 2022, "category": "tech"})
    docs.add("d10", "练 习 册", {"year": 2022, "category": "life"})
    rules = RulesStore()
    rules.add("unicode-unigram-v1", "idf-ln-v1")   # 规则版本 1
    stats = StatsStore()
    engine = Engine(docs, stats, rules)
    engine.build_stats()                            # 统计版本 1
    return docs, stats, rules, engine


def show_verdict(label, verdict):
    print(f"  [{label}] 判定: {verdict['verdict']}")
    if verdict.get("first_failure"):
        ff = verdict["first_failure"]
        print(f"    首个不成立节点: {ff['path']}")
        print(f"    理由: {ff['reason']}")
    if verdict["verdict"] == "unverifiable":
        print(f"    理由: {verdict['reason']}")


def main():
    docs, stats, rules, engine = build_world()
    verifier = Verifier(docs, stats, rules)

    print("=" * 72)
    print("1) 检索并产出解释树")
    print("=" * 72)
    expl = engine.search(**QUERY)
    print(f"  rules_version={expl['rules_version']}  stats_version={expl['stats_version']}  "
          f"corpus_version={expl['corpus_version']}  k={expl['k']}")
    print(f"  解释树根哈希: {expl['hash'][:16]}...")
    for child in expl["children"]:
        if child["type"] != "result":
            continue
        parts = [f"{c['term']}({c['weight']}*{c['tf']}*{c['idf']:.4f}={c['contribution']:.4f})"
                 for c in child["children"]]
        total = 0.0
        for c in child["children"]:
            total += c["contribution"]
        print(f"  命中 rank={child['rank']} doc={child['doc_id']} score={child['score']:.6f}")
        print(f"    分项: {' + '.join(parts)}")
        print(f"    分项合计={total:.6f}  等于最终分数: {total == child['score']}")
    for child in expl["children"]:
        if child["type"] == "pruned":
            print(f"  剪枝节点样例 doc={child['doc_id']}: upper_bound={child['upper_bound']:.6f} "
                  f"<= threshold={child['threshold']:.6f} (kth={child['kth_doc_id']}), "
                  f"统计版本={expl['stats_version']}")
            print(f"    {json.dumps({k: child[k] for k in ('processed', 'remaining')}, ensure_ascii=False)}")

    print()
    print("=" * 72)
    print("2) 指定文档未命中原因查询（不是笼统的“无结果”）")
    print("=" * 72)
    for doc_id in ("d4", "d6", "d9", "d10", "d1", "d404"):
        print(f"  {doc_id}: {json.dumps(explain_miss(expl, doc_id), ensure_ascii=False)}")

    print()
    print("=" * 72)
    print("3) 独立验证器复算当前解释")
    print("=" * 72)
    show_verdict("原始解释", verifier.verify(expl))

    print()
    print("=" * 72)
    print("4) 统计变更 + 规则更新后，旧解释保持历史有效")
    print("=" * 72)
    docs.add("d11", "Python 机器学习 进阶 新 文档", {"year": 2024, "category": "tech"})
    engine.build_stats()                            # 统计版本 2
    rules.add("unicode-unigram-v1", "idf-bm25-v2")  # 规则版本 2（更换 idf 公式）
    expl_v2 = engine.search(**QUERY)
    print(f"  新解释绑定: rules_version={expl_v2['rules_version']} "
          f"stats_version={expl_v2['stats_version']} corpus_version={expl_v2['corpus_version']}")
    print(f"  旧解释绑定: rules_version={expl['rules_version']} "
          f"stats_version={expl['stats_version']} corpus_version={expl['corpus_version']}")
    show_verdict("统计/规则更新后的旧解释", verifier.verify(expl))
    show_verdict("新规则下的新解释", verifier.verify(expl_v2))

    print()
    print("=" * 72)
    print("5) 注入篡改（攻击者会重算哈希掩盖痕迹，验证器靠复算抓出）")
    print("=" * 72)
    # 5a) 篡改权重
    tampered = copy.deepcopy(expl)
    term_node = tampered["children"][0]["children"][0]
    term_node["weight"] = 99.0
    term_node["contribution"] = 99.0 * term_node["tf"] * term_node["idf"]
    finalize(tampered)  # 攻击者重算哈希
    show_verdict("篡改权重", verifier.verify(tampered))
    # 5b) 遗漏过滤节点
    tampered = copy.deepcopy(expl)
    tampered["children"] = [c for c in tampered["children"] if not (
        c["type"] == "filtered" and c["doc_id"] == "d4")]
    finalize(tampered)
    show_verdict("遗漏过滤节点", verifier.verify(tampered))
    # 5c) 替换文档版本（正常替换：旧解释仍历史有效）
    docs.update("d1", "Python 机器学习 进阶：深度 学习 框架 实战")
    show_verdict("文档正常升级后验证旧解释", verifier.verify(expl))
    # 5d) 替换文档版本（恶意篡改历史修订内容）
    # 保持词项多重集合不变（绕过统计复算），但内容哈希必然变化
    docs._docs["d1"][0]["text"] = "项目 实战 机器学习 做 Python 用：入门 机器学习 Python"
    show_verdict("历史文档内容被篡改", verifier.verify(expl))
    docs._docs["d1"][0]["text"] = "Python 机器学习 入门：用 Python 做 机器学习 实战 项目"

    print()
    print("=" * 72)
    print("6) 缺统计证据 -> 只能判不可验证")
    print("=" * 72)
    stats.drop(expl["stats_version"])
    show_verdict("统计快照被清除后的旧解释", verifier.verify(expl))


if __name__ == "__main__":
    main()
