"""稳定 ID 与版本目录：事实类型、规则、步骤、证书模式。

所有事实、规则与步骤都带稳定 ID 和版本；本模块是唯一权威目录。
"""

SCHEMA_DOMAIN = "evidence-gc"
SCHEMA_VERSION = 1

# ---- 事实类型（stable id, version）----
FACT_TYPES = {
    "evidence.instance": {"version": 1, "desc": "证据实例（eid, version 为删除单元）"},
    "evidence.reference": {"version": 1, "desc": "证据间引用（钉住目标版本，可失效）"},
    "freeze.marker": {"version": 1, "desc": "冻结标记（可继承、可撤回）"},
    "retention.policy": {"version": 1, "desc": "自定义保留期策略（法定保留不在本系统范围）"},
    "gc.plan": {"version": 1, "desc": "标记代际计划（含版本快照）"},
    "gc.certificate": {"version": 1, "desc": "可重放删除证书"},
    "liveness.proof": {"version": 1, "desc": "存活证明（无悬空依赖）"},
}

# ---- 规则（stable id, version）----
RULES = {
    "R-RET-1": {
        "version": 1,
        "desc": "自定义保留期：created_tick + retention_ticks > now 时证据被保留（法定保留不适用）",
    },
    "R-REF-1": {
        "version": 1,
        "desc": "有效引用：active 且 target_version 等于目标当前版本；有效引用沿边保留目标",
    },
    "R-FRZ-1": {
        "version": 1,
        "desc": "显式冻结：active 冻结标记保留目标实例，并沿有效引用继承到被引用实例",
    },
    "R-FRZ-2": {
        "version": 1,
        "desc": "冻结可撤回：撤回后重新计算闭包，仅由冻结保留的实例转为可回收",
    },
    "R-CLO-1": {
        "version": 1,
        "desc": "最小不可回收闭包：保留策略与引用版本联合求不动点，取满足规则的最小集合",
    },
    "R-GC-1": {
        "version": 1,
        "desc": "可回收集合 = 全部实例 - 最小不可回收闭包；过期但被活引用者不可回收",
    },
    "R-NEW-1": {
        "version": 1,
        "desc": "新引用/新冻结/保留期延长在提交前到来时，必须阻止已计划删除",
    },
    "R-CMT-1": {
        "version": 1,
        "desc": "提交验证：代际一致、候选仍存在且哈希一致、版本快照一致，否则拒绝",
    },
    "R-PRF-1": {
        "version": 1,
        "desc": "存活证明：提交后每个存活实例有保留依据，且每条有效引用指向存活实例",
    },
    "R-DRY-1": {
        "version": 1,
        "desc": "试算（dry-run）只读不删除；任何应用前必须验证版本",
    },
}

# ---- 步骤（stable id, version）----
STEPS = {
    "S1": {"version": 1, "desc": "标记代际：生成 plan，固化版本快照与候选集"},
    "S2": {"version": 1, "desc": "提交前验证：代际、存在性、内容哈希、版本快照"},
    "S3": {"version": 1, "desc": "重算闭包：新引用/解冻等到来后重新计算"},
    "S4": {"version": 1, "desc": "删除：仅删除重算后仍可回收的候选，其余阻止并记录"},
    "S5": {"version": 1, "desc": "签发：输出删除证书、阻塞链与存活证明"},
}

CERTIFICATE_KIND = "gc.deletion_certificate"
CERTIFICATE_VERSION = 1
