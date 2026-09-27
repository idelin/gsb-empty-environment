"""Versioned time-interval authorization engine with CLI.

Implements:
- Allow/deny rules with subject, resource prefix, action and validity interval.
- Deny-overrides-allow evaluation.
- Resource path normalization (dot segments, duplicate separators) with
  escape-above-root rejection.
- Audit records that pin the policy (rule-set) version used for a decision.
- Earliest permission-difference search between two policy versions over a
  finite resource set, including a witness request.

Python 3.11 standard library only.
"""

from __future__ import annotations

import argparse
import json
import sys
from dataclasses import dataclass
from datetime import datetime, timedelta, timezone
from typing import Iterable, Optional

EPOCH = datetime(1970, 1, 1, tzinfo=timezone.utc)
PROBE_SUBJECT = "__probe_subject__"
PROBE_ACTION = "__probe_action__"


class PathEscapesRoot(ValueError):
    """Raised when a normalized path would escape above the root."""


def parse_time(text: Optional[str]) -> Optional[datetime]:
    """Parse an ISO 8601 timestamp; None means unbounded."""
    if text is None:
        return None
    normalized = text.strip()
    if normalized.endswith("Z"):
        normalized = normalized[:-1] + "+00:00"
    parsed = datetime.fromisoformat(normalized)
    if parsed.tzinfo is None:
        parsed = parsed.replace(tzinfo=timezone.utc)
    return parsed.astimezone(timezone.utc)


def format_time(moment: datetime) -> str:
    return moment.astimezone(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def normalize_path(path: str) -> str:
    """Normalize a resource path.

    - Collapses duplicate separators and resolves "." / ".." segments.
    - Requires an absolute path (leading "/").
    - Raises PathEscapesRoot if ".." would climb above the root.
    """
    if not isinstance(path, str) or not path.startswith("/"):
        raise ValueError(f"resource path must be absolute: {path!r}")
    segments: list[str] = []
    for segment in path.split("/"):
        if segment in ("", "."):
            continue
        if segment == "..":
            if not segments:
                raise PathEscapesRoot(f"path escapes root: {path!r}")
            segments.pop()
        else:
            segments.append(segment)
    return "/" + "/".join(segments)


def normalize_pattern(pattern: str) -> str:
    """Normalize a resource prefix pattern.

    "*" is shorthand for "/*" (matches everything). A trailing "/*" marks a
    prefix pattern; anything else is an exact path pattern.
    """
    if pattern == "*":
        return "/*"
    if pattern.endswith("/*"):
        base = normalize_path(pattern[:-2] or "/")
        return base.rstrip("/") + "/*" if base != "/" else "/*"
    return normalize_path(pattern)


def prefix_match(pattern: str, path: str) -> bool:
    """Check whether normalized path matches normalized pattern."""
    if pattern == "/*":
        return True
    if pattern.endswith("/*"):
        base = pattern[:-2]
        return path == base or path.startswith(base + "/")
    return path == pattern


def _field_match(rule_value: str, request_value: str) -> bool:
    return rule_value == "*" or rule_value == request_value


@dataclass(frozen=True)
class Rule:
    rule_id: str
    effect: str  # "allow" | "deny"
    subject: str
    resource_pattern: str
    action: str
    valid_from: Optional[datetime]
    valid_to: Optional[datetime]

    def active_at(self, moment: datetime) -> bool:
        if self.valid_from is not None and moment < self.valid_from:
            return False
        if self.valid_to is not None and moment >= self.valid_to:
            return False
        return True

    def matches(self, subject: str, resource: str, action: str) -> bool:
        return (
            _field_match(self.subject, subject)
            and _field_match(self.action, action)
            and prefix_match(self.resource_pattern, resource)
        )

    @staticmethod
    def from_dict(data: dict) -> "Rule":
        effect = data["effect"]
        if effect not in ("allow", "deny"):
            raise ValueError(f"invalid effect: {effect!r}")
        return Rule(
            rule_id=str(data["id"]),
            effect=effect,
            subject=str(data["subject"]),
            resource_pattern=normalize_pattern(str(data["resource_prefix"])),
            action=str(data["action"]),
            valid_from=parse_time(data.get("valid_from")),
            valid_to=parse_time(data.get("valid_to")),
        )


@dataclass(frozen=True)
class Policy:
    version: str
    rules: tuple[Rule, ...]

    @staticmethod
    def from_dict(data: dict) -> "Policy":
        rules = tuple(Rule.from_dict(item) for item in data.get("rules", []))
        return Policy(version=str(data["version"]), rules=rules)

    @staticmethod
    def load(path: str) -> "Policy":
        with open(path, "r", encoding="utf-8") as handle:
            return Policy.from_dict(json.load(handle))


def decide(
    policy: Policy,
    subject: str,
    resource: str,
    action: str,
    moment: datetime,
) -> dict:
    """Evaluate a request; deny overrides allow. Returns an audit record."""
    try:
        normalized = normalize_path(resource)
    except (ValueError, PathEscapesRoot) as exc:
        return {
            "policy_version": policy.version,
            "decision": "deny",
            "reason": f"invalid_resource: {exc}",
            "request": {
                "subject": subject,
                "resource": resource,
                "action": action,
                "time": format_time(moment),
            },
            "matched_rules": [],
        }

    matched_allow: list[Rule] = []
    matched_deny: list[Rule] = []
    for rule in policy.rules:
        if rule.active_at(moment) and rule.matches(subject, normalized, action):
            (matched_deny if rule.effect == "deny" else matched_allow).append(rule)

    if matched_deny:
        decision, reason, fired = "deny", "explicit_deny", matched_deny
    elif matched_allow:
        decision, reason, fired = "allow", "explicit_allow", matched_allow
    else:
        decision, reason, fired = "deny", "implicit_deny", []

    return {
        "policy_version": policy.version,
        "decision": decision,
        "reason": reason,
        "request": {
            "subject": subject,
            "resource": resource,
            "normalized_resource": normalized,
            "action": action,
            "time": format_time(moment),
        },
        "matched_rules": [
            {"id": rule.rule_id, "version": policy.version, "effect": rule.effect}
            for rule in fired
        ],
    }


def _candidate_times(policies: Iterable[Policy]) -> tuple[list[datetime], set[datetime]]:
    """Decision-relevant probe times plus the set of synthetic probes.

    Decisions are piecewise constant in time, changing only at interval
    boundaries, so probing every boundary plus one point before the first
    and one after the last covers the whole timeline. The two outer probes
    are synthetic representatives of unbounded regions.
    """
    boundaries: set[datetime] = set()
    for policy in policies:
        for rule in policy.rules:
            if rule.valid_from is not None:
                boundaries.add(rule.valid_from)
            if rule.valid_to is not None:
                boundaries.add(rule.valid_to)
    if not boundaries:
        return [EPOCH], {EPOCH}
    ordered = sorted(boundaries)
    step = timedelta(seconds=1)
    synthetic = {ordered[0] - step, ordered[-1] + step}
    return [ordered[0] - step] + ordered + [ordered[-1] + step], synthetic


def _probe_values(values: Iterable[str], probe: str) -> list[str]:
    concrete = sorted({value for value in values if value != "*"})
    uses_wildcard = any(value == "*" for value in values)
    if uses_wildcard or not concrete:
        concrete.append(probe)
    return concrete


def earliest_difference(
    policy_a: Policy,
    policy_b: Policy,
    resources: Iterable[str],
) -> Optional[dict]:
    """Find the earliest time at which the two policy versions disagree.

    Searches over the finite resource set and the finite subject/action
    domains induced by the rules (plus synthetic probes for wildcards).
    Returns a witness request, or None if the versions are equivalent.
    """
    normalized_resources = sorted({normalize_path(r) for r in resources})
    all_rules = list(policy_a.rules) + list(policy_b.rules)
    subjects = _probe_values((r.subject for r in all_rules), PROBE_SUBJECT)
    actions = _probe_values((r.action for r in all_rules), PROBE_ACTION)

    moments, synthetic = _candidate_times((policy_a, policy_b))
    for moment in moments:
        for resource in normalized_resources:
            for subject in subjects:
                for action in actions:
                    result_a = decide(policy_a, subject, resource, action, moment)
                    result_b = decide(policy_b, subject, resource, action, moment)
                    if result_a["decision"] != result_b["decision"]:
                        outcome = {
                            "time": format_time(moment),
                            "witness": {
                                "subject": subject,
                                "resource": resource,
                                "action": action,
                            },
                            "decisions": {
                                policy_a.version: result_a["decision"],
                                policy_b.version: result_b["decision"],
                            },
                        }
                        if moment in synthetic:
                            outcome["note"] = (
                                "difference holds throughout an unbounded "
                                "region; time is a representative probe"
                            )
                        return outcome
    return None


def _cmd_authorize(args: argparse.Namespace) -> int:
    policy = Policy.load(args.policy)
    moment = parse_time(args.time) if args.time else datetime.now(timezone.utc)
    record = decide(policy, args.subject, args.resource, args.action, moment)
    json.dump(record, sys.stdout, indent=2, ensure_ascii=False)
    sys.stdout.write("\n")
    return 0 if record["decision"] == "allow" else 1


def _cmd_diff(args: argparse.Namespace) -> int:
    policy_a = Policy.load(args.policy_a)
    policy_b = Policy.load(args.policy_b)
    with open(args.resources, "r", encoding="utf-8") as handle:
        resources = json.load(handle)
    if not isinstance(resources, list) or not all(
        isinstance(item, str) for item in resources
    ):
        raise SystemExit("resources file must be a JSON array of path strings")
    outcome = earliest_difference(policy_a, policy_b, resources)
    if outcome is None:
        json.dump(
            {
                "result": "equivalent",
                "versions": [policy_a.version, policy_b.version],
            },
            sys.stdout,
            indent=2,
            ensure_ascii=False,
        )
        sys.stdout.write("\n")
        return 0
    json.dump(
        {
            "result": "different",
            "versions": [policy_a.version, policy_b.version],
            "earliest_difference": outcome,
        },
        sys.stdout,
        indent=2,
        ensure_ascii=False,
    )
    sys.stdout.write("\n")
    return 2


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="authz",
        description="Versioned time-interval authorization engine.",
    )
    sub = parser.add_subparsers(dest="command", required=True)

    auth = sub.add_parser("authorize", help="evaluate a single request")
    auth.add_argument("--policy", required=True, help="policy JSON file")
    auth.add_argument("--subject", required=True)
    auth.add_argument("--resource", required=True)
    auth.add_argument("--action", required=True)
    auth.add_argument(
        "--time",
        default=None,
        help="ISO 8601 timestamp (default: current UTC time)",
    )
    auth.set_defaults(func=_cmd_authorize)

    diff = sub.add_parser(
        "diff", help="earliest permission difference between two versions"
    )
    diff.add_argument("--policy-a", required=True, help="policy JSON (version A)")
    diff.add_argument("--policy-b", required=True, help="policy JSON (version B)")
    diff.add_argument(
        "--resources",
        required=True,
        help="JSON file containing an array of resource paths",
    )
    diff.set_defaults(func=_cmd_diff)
    return parser


def main(argv: Optional[list[str]] = None) -> int:
    args = build_parser().parse_args(argv)
    return args.func(args)


if __name__ == "__main__":
    raise SystemExit(main())
