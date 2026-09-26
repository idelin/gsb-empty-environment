"""Versioned time-interval authorization engine.

Core concepts:
- A *policy* is a named, versioned set of rules.
- A *rule* (allow or deny) has a subject, a resource prefix, an action and a
  validity interval ``[start, end)`` (either bound may be unbounded).
- Evaluation normalizes the resource path (dot segments, duplicate
  separators, clamped at the root so ``..`` can never escape it), then
  applies *deny overrides*: any matching deny rule wins over allows.
  With no matching allow the default decision is deny.
- Every evaluation returns an audit record pinned to the policy version.
- ``earliest_difference`` compares two policy versions over a finite set of
  resources and returns the earliest time at which their decisions diverge,
  together with a witness request.

Only the Python standard library is used (Python 3.11+).
"""

from __future__ import annotations

import argparse
import json
import sys
from dataclasses import dataclass
from datetime import datetime, timezone
from typing import Iterable, Optional, Sequence

EFFECT_ALLOW = "allow"
EFFECT_DENY = "deny"

# Sentinel used by the diff engine to represent "any time before the first
# rule boundary".  Decisions are piecewise constant and may only change at
# rule interval boundaries, so one probe before all boundaries suffices.
BEGINNING_OF_TIME = datetime.min.replace(tzinfo=timezone.utc)


# ---------------------------------------------------------------------------
# Path normalization
# ---------------------------------------------------------------------------

def normalize_path(path: str) -> str:
    """Normalize a resource path.

    - Duplicate separators are collapsed (``/a//b`` -> ``/a/b``).
    - ``.`` segments are removed.
    - ``..`` pops the last segment, clamped at the root: it is impossible to
      escape above ``/`` (``/a/../../etc`` -> ``/etc``).
    - The result always starts with a single ``/`` and has no trailing slash
      (except the root itself).
    """
    if not isinstance(path, str) or not path.strip():
        raise ValueError("resource path must be a non-empty string")
    segments: list[str] = []
    for part in path.split("/"):
        if part in ("", "."):
            continue
        if part == "..":
            if segments:
                segments.pop()
            # ".." at the root is discarded: escaping the root is forbidden.
            continue
        segments.append(part)
    return "/" + "/".join(segments)


def prefix_matches(prefix: str, resource: str) -> bool:
    """Segment-aware prefix match.

    ``/root/private`` matches ``/root/private`` and ``/root/private/x`` but
    not ``/root/privatex``.  The root prefix ``/`` matches everything.
    Both arguments are expected to be normalized already.
    """
    if prefix == "/":
        return True
    return resource == prefix or resource.startswith(prefix + "/")


# ---------------------------------------------------------------------------
# Time parsing
# ---------------------------------------------------------------------------

def parse_time(value: object) -> Optional[datetime]:
    """Parse an ISO-8601 timestamp (``None``/``null`` means unbounded).

    Naive timestamps are interpreted as UTC.
    """
    if value is None:
        return None
    if not isinstance(value, str):
        raise ValueError(f"invalid timestamp: {value!r}")
    dt = datetime.fromisoformat(value)  # Python 3.11+ accepts a "Z" suffix.
    if dt.tzinfo is None:
        dt = dt.replace(tzinfo=timezone.utc)
    return dt


def format_time(dt: Optional[datetime]) -> Optional[str]:
    if dt is None:
        return None
    return dt.isoformat()


# ---------------------------------------------------------------------------
# Rules and policies
# ---------------------------------------------------------------------------

@dataclass(frozen=True)
class Rule:
    rule_id: str
    effect: str  # "allow" or "deny"
    subject: str  # exact subject or "*" wildcard
    action: str  # exact action or "*" wildcard
    prefix: str  # normalized resource prefix
    start: Optional[datetime]  # inclusive; None = unbounded past
    end: Optional[datetime]  # exclusive; None = unbounded future

    def __post_init__(self) -> None:
        if self.effect not in (EFFECT_ALLOW, EFFECT_DENY):
            raise ValueError(f"invalid effect: {self.effect!r}")
        if self.start is not None and self.end is not None and self.start >= self.end:
            raise ValueError(f"rule {self.rule_id!r}: empty validity interval")

    def matches(self, subject: str, action: str, resource: str, when: datetime) -> bool:
        if self.subject != "*" and self.subject != subject:
            return False
        if self.action != "*" and self.action != action:
            return False
        if not prefix_matches(self.prefix, resource):
            return False
        if self.start is not None and when < self.start:
            return False
        if self.end is not None and when >= self.end:
            return False
        return True

    @staticmethod
    def from_dict(data: dict, index: int = 0) -> "Rule":
        try:
            return Rule(
                rule_id=str(data.get("id", f"rule-{index}")),
                effect=str(data["effect"]).lower(),
                subject=str(data.get("subject", "*")),
                action=str(data.get("action", "*")),
                prefix=normalize_path(str(data["prefix"])),
                start=parse_time(data.get("start")),
                end=parse_time(data.get("end")),
            )
        except KeyError as exc:
            raise ValueError(f"rule #{index} is missing field {exc}") from exc


@dataclass(frozen=True)
class AuditRecord:
    version: str
    decision: str
    subject: str
    action: str
    resource: str  # normalized resource actually evaluated
    time: Optional[datetime]
    matched_allow: tuple[str, ...]
    matched_deny: tuple[str, ...]
    reason: str

    def to_dict(self) -> dict:
        return {
            "version": self.version,
            "decision": self.decision,
            "subject": self.subject,
            "action": self.action,
            "resource": self.resource,
            "time": format_time(self.time),
            "matched_allow": list(self.matched_allow),
            "matched_deny": list(self.matched_deny),
            "reason": self.reason,
        }


@dataclass(frozen=True)
class Policy:
    version: str
    rules: tuple[Rule, ...]

    def evaluate(self, subject: str, resource: str, action: str,
                 when: Optional[datetime]) -> AuditRecord:
        """Evaluate a request. Deny rules take precedence over allow rules."""
        normalized = normalize_path(resource)
        moment = when if when is not None else BEGINNING_OF_TIME
        matched = [r for r in self.rules if r.matches(subject, action, normalized, moment)]
        denies = tuple(r.rule_id for r in matched if r.effect == EFFECT_DENY)
        allows = tuple(r.rule_id for r in matched if r.effect == EFFECT_ALLOW)
        if denies:
            decision, reason = EFFECT_DENY, "matched deny rule (deny overrides)"
        elif allows:
            decision, reason = EFFECT_ALLOW, "matched allow rule"
        else:
            decision, reason = EFFECT_DENY, "no matching allow rule (default deny)"
        return AuditRecord(
            version=self.version,
            decision=decision,
            subject=subject,
            action=action,
            resource=normalized,
            time=when,
            matched_allow=allows,
            matched_deny=denies,
            reason=reason,
        )

    @staticmethod
    def from_dict(data: dict) -> "Policy":
        if "version" not in data:
            raise ValueError("policy is missing field 'version'")
        rules = tuple(Rule.from_dict(r, i) for i, r in enumerate(data.get("rules", [])))
        return Policy(version=str(data["version"]), rules=rules)


def load_policy(path: str) -> Policy:
    with open(path, "r", encoding="utf-8") as fh:
        return Policy.from_dict(json.load(fh))


# ---------------------------------------------------------------------------
# Cross-version diff
# ---------------------------------------------------------------------------

def _boundary_times(policies: Iterable[Policy]) -> list[datetime]:
    points = {BEGINNING_OF_TIME}
    for policy in policies:
        for rule in policy.rules:
            if rule.start is not None:
                points.add(rule.start)
            if rule.end is not None:
                points.add(rule.end)
    return sorted(points)


def earliest_difference(
    policy_a: Policy,
    policy_b: Policy,
    resources: Sequence[str],
    subjects: Optional[Sequence[str]] = None,
    actions: Optional[Sequence[str]] = None,
) -> Optional[dict]:
    """Find the earliest time at which two policy versions disagree.

    The search space is the given finite resource set crossed with subjects
    and actions (default: every subject/action literal mentioned by either
    policy).  Because decisions only change at rule interval boundaries,
    probing each boundary (plus one point before all boundaries) is exact.

    Returns ``None`` if the versions are equivalent over the search space,
    otherwise a dict with the earliest divergence time and a witness request.
    """
    if subjects is None:
        subjects = sorted({r.subject for p in (policy_a, policy_b) for r in p.rules})
    if actions is None:
        actions = sorted({r.action for p in (policy_a, policy_b) for r in p.rules})
    normalized_resources = sorted({normalize_path(r) for r in resources})
    if not normalized_resources:
        raise ValueError("resource set must be non-empty")

    for moment in _boundary_times((policy_a, policy_b)):
        for subject in subjects:
            for action in actions:
                for resource in normalized_resources:
                    rec_a = policy_a.evaluate(subject, resource, action, moment)
                    rec_b = policy_b.evaluate(subject, resource, action, moment)
                    if rec_a.decision != rec_b.decision:
                        return {
                            "found": True,
                            "time": format_time(moment),
                            "note": (
                                "decisions are constant between rule boundaries; "
                                "this is the earliest divergence point"
                            ),
                            "request": {
                                "subject": subject,
                                "action": action,
                                "resource": resource,
                                "time": format_time(moment),
                            },
                            "version_a": policy_a.version,
                            "decision_a": rec_a.decision,
                            "version_b": policy_b.version,
                            "decision_b": rec_b.decision,
                        }
    return None


# ---------------------------------------------------------------------------
# Command line interface
# ---------------------------------------------------------------------------

def _cmd_check(args: argparse.Namespace) -> int:
    policy = load_policy(args.policy)
    record = policy.evaluate(
        subject=args.subject,
        resource=args.resource,
        action=args.action,
        when=parse_time(args.time),
    )
    json.dump(record.to_dict(), sys.stdout, indent=2, ensure_ascii=False)
    sys.stdout.write("\n")
    return 0 if record.decision == EFFECT_ALLOW else 1


def _cmd_diff(args: argparse.Namespace) -> int:
    policy_a = load_policy(args.policy_a)
    policy_b = load_policy(args.policy_b)
    result = earliest_difference(
        policy_a,
        policy_b,
        resources=args.resources,
        subjects=args.subjects or None,
        actions=args.actions or None,
    )
    if result is None:
        result = {
            "found": False,
            "version_a": policy_a.version,
            "version_b": policy_b.version,
            "note": "versions are equivalent over the given resources/subjects/actions",
        }
    json.dump(result, sys.stdout, indent=2, ensure_ascii=False)
    sys.stdout.write("\n")
    return 0 if result["found"] else 1


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="authz",
        description="Versioned time-interval authorization engine.",
    )
    sub = parser.add_subparsers(dest="command", required=True)

    check = sub.add_parser("check", help="evaluate one request against a policy")
    check.add_argument("--policy", required=True, help="path to policy JSON file")
    check.add_argument("--subject", required=True)
    check.add_argument("--resource", required=True)
    check.add_argument("--action", required=True)
    check.add_argument("--time", default=None,
                       help="ISO-8601 timestamp (default: unbounded/any time)")
    check.set_defaults(func=_cmd_check)

    diff = sub.add_parser("diff", help="earliest divergence between two policy versions")
    diff.add_argument("--policy-a", required=True, help="path to policy JSON (version A)")
    diff.add_argument("--policy-b", required=True, help="path to policy JSON (version B)")
    diff.add_argument("--resources", required=True, nargs="+",
                      help="finite set of resource paths to compare")
    diff.add_argument("--subjects", nargs="*", default=None,
                      help="subjects to compare (default: all subjects in the rules)")
    diff.add_argument("--actions", nargs="*", default=None,
                      help="actions to compare (default: all actions in the rules)")
    diff.set_defaults(func=_cmd_diff)
    return parser


def main(argv: Optional[Sequence[str]] = None) -> int:
    args = build_parser().parse_args(argv)
    try:
        return args.func(args)
    except (ValueError, KeyError, OSError, json.JSONDecodeError) as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
