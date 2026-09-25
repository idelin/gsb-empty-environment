"""合成样例：账户风控场景，两个观察时刻 t1 -> t2。

t2 相对 t1 的输入变化（每类各至少一例）：
  - device-trusted 被撤回            -> 证据失效（eligible / fee-waiver 失效）
  - vip-status 新增                  -> 新增事实（priority-support 出现）
  - R6 内容变更（同 ID，v1 -> v2）   -> 规则变更（audit-flag 失效）
  - kyc-complete 更新（仍满足条件）  -> account-ok 结果相同但支持来源改变
  - promo-code 新增 + R10 新增       -> 无关输入（推不出任何结论，不进入解释）
"""

from causal.model import Cond, Fact, Rule, Snapshot


def build_snapshots():
    facts_t1 = [
        Fact.make("kyc-complete", 1, status="ok"),
        Fact.make("device-trusted", 1, level=3),
        Fact.make("txn-volume", 1, amount=120000),
        Fact.make("marketing-opt-in", 1, channel="email"),
    ]
    rules_t1 = [
        Rule.make("R1", 1,
                  [Cond("kyc-complete", "status", "eq", "ok"),
                   Cond("device-trusted", "level", "ge", 2)],
                  "eligible"),
        Rule.make("R2", 1,
                  [Cond.present("eligible"),
                   Cond("txn-volume", "amount", "ge", 100000)],
                  "fee-waiver"),
        Rule.make("R5", 1,
                  [Cond("vip-status", "tier", "eq", "gold")],
                  "priority-support"),
        Rule.make("R6", 1,
                  [Cond("txn-volume", "amount", "ge", 50000)],
                  "audit-flag"),
        Rule.make("R7", 1,
                  [Cond.present("kyc-complete")],
                  "account-ok"),
        Rule.make("R8", 1,
                  [Cond.present("marketing-opt-in")],
                  "newsletter"),
    ]

    facts_t2 = [
        Fact.make("kyc-complete", 2, status="ok", reviewer="auto"),  # 更新：仍满足 R1/R7
        # device-trusted 被撤回（证据失效）
        Fact.make("txn-volume", 1, amount=120000),
        Fact.make("marketing-opt-in", 1, channel="email"),
        Fact.make("vip-status", 1, tier="gold"),   # 新增事实
        Fact.make("promo-code", 1, code="PROMO9"),  # 无关输入：无可用规则链
    ]
    rules_t2 = [
        Rule.make("R1", 1,
                  [Cond("kyc-complete", "status", "eq", "ok"),
                   Cond("device-trusted", "level", "ge", 2)],
                  "eligible"),
        Rule.make("R2", 1,
                  [Cond.present("eligible"),
                   Cond("txn-volume", "amount", "ge", 100000)],
                  "fee-waiver"),
        Rule.make("R5", 1,
                  [Cond("vip-status", "tier", "eq", "gold")],
                  "priority-support"),
        Rule.make("R6", 2,  # 同 ID 内容变更：阈值 50000 -> 200000，按版本识别
                  [Cond("txn-volume", "amount", "ge", 200000)],
                  "audit-flag"),
        Rule.make("R7", 1,
                  [Cond.present("kyc-complete")],
                  "account-ok"),
        Rule.make("R8", 1,
                  [Cond.present("marketing-opt-in")],
                  "newsletter"),
        Rule.make("R10", 1,  # 无关规则：条件永远不满足，推不出结论
                  [Cond.present("promo-code"),
                   Cond.present("loyalty-years")],
                  "promo-eligible"),
    ]

    return Snapshot("t1", facts_t1, rules_t1), Snapshot("t2", facts_t2, rules_t2)
