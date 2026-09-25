"""Synthetic sample scenario (all data is fabricated for demonstration).

Conclusion CONC-001 (rule RULE-001 v1) requires, jointly:
  * category "lab":   at least 2 independent sources
  * category "field": at least 1 independent source
with cross-category source exclusivity, over the domain [0, 30).

Evidence layout (half-open intervals):
  EV-01  S-A lab   [ 0, 20)          EV-02  S-A lab   [20, 30)  (touching)
  EV-03  S-B lab   [ 5, 25)          EV-04  S-B lab   [25, 30)  (touching)
  EV-05  S-C lab   [10, 30) expires at 15  -> only counts on [10, 15)
  EV-06  S-D field [ 0, 30)
  EV-07  S-B field [ 0, 30)  (S-B is exclusive: usable in only one category)

Expected: lab has 2 independent sources only from t=5 on (S-A joins S-B),
so the satisfied interval is [5, 30), the gap is [0, 5), and retracting
S-B's lab evidence shrinks satisfaction to [10, 15) (S-C's live window).
"""

DOMAIN = (0, 30)

STEPS = [
    {
        "step_id": "STEP-001",
        "version": 1,
        "op": "declare_rule",
        "rule": {
            "rule_id": "RULE-001",
            "version": 1,
            "conclusion": "CONC-001",
            "exclusive_sources": True,
            "requirements": [
                {"category": "lab", "min_sources": 2},
                {"category": "field", "min_sources": 1},
            ],
        },
    },
    {"step_id": "STEP-002", "version": 1, "op": "add_evidence",
     "evidence": {"evidence_id": "EV-01", "version": 1, "source_id": "S-A",
                  "category": "lab", "start": 0, "end": 20}},
    {"step_id": "STEP-003", "version": 1, "op": "add_evidence",
     "evidence": {"evidence_id": "EV-02", "version": 1, "source_id": "S-A",
                  "category": "lab", "start": 20, "end": 30}},
    {"step_id": "STEP-004", "version": 1, "op": "add_evidence",
     "evidence": {"evidence_id": "EV-03", "version": 1, "source_id": "S-B",
                  "category": "lab", "start": 5, "end": 25}},
    {"step_id": "STEP-005", "version": 1, "op": "add_evidence",
     "evidence": {"evidence_id": "EV-04", "version": 1, "source_id": "S-B",
                  "category": "lab", "start": 25, "end": 30}},
    {"step_id": "STEP-006", "version": 1, "op": "add_evidence",
     "evidence": {"evidence_id": "EV-05", "version": 1, "source_id": "S-C",
                  "category": "lab", "start": 10, "end": 30, "expires_at": 15}},
    {"step_id": "STEP-007", "version": 1, "op": "add_evidence",
     "evidence": {"evidence_id": "EV-06", "version": 1, "source_id": "S-D",
                  "category": "field", "start": 0, "end": 30}},
    {"step_id": "STEP-008", "version": 1, "op": "add_evidence",
     "evidence": {"evidence_id": "EV-07", "version": 1, "source_id": "S-B",
                  "category": "field", "start": 0, "end": 30}},
    {"step_id": "STEP-009", "version": 1, "op": "evaluate",
     "rule_id": "RULE-001", "domain": DOMAIN},
    {"step_id": "STEP-010", "version": 1, "op": "minimal_subset",
     "rule_id": "RULE-001", "target": (5, 30)},
    # Retraction demo: remove S-B's lab evidence (its last lab supporters).
    {"step_id": "STEP-011", "version": 1, "op": "retract_evidence",
     "evidence_id": "EV-03"},
    {"step_id": "STEP-012", "version": 1, "op": "retract_evidence",
     "evidence_id": "EV-04"},
    {"step_id": "STEP-013", "version": 1, "op": "evaluate",
     "rule_id": "RULE-001", "domain": DOMAIN},
]
