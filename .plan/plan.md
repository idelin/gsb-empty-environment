# 最小费用整数流（容量上下界 + 负费用 + 不可行证据）

1. mincost_flow.py：Fraction 精确运算；下界预处理改平衡；超源超汇 + Dinic 判可行（割作证据）；Bellman-Ford 找负费用环 + 消圈法求最优；三态结果（可行/最优/预算）
2. demo.py：可行+负环、预算判定、不可行（供需不等）、结构不可行（容量瓶颈）
3. test_mincost_flow.py：unittest 覆盖 11 个场景
4. Python 3.11 下运行测试与样例，记录输出到 README.md
