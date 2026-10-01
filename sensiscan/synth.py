"""Synthetic reference data and independently annotated expectations.

All identifiers below are invented for testing only (RFC 2606 domain,
obviously fake SSN/token). Annotations record (path, rule_id, value)
triples that a correct scanner must surface; controls are similar but
non-equal values that must NOT be flagged by dictionary rules.
"""

from __future__ import annotations

from dataclasses import dataclass

SSN = "123-45-6789"
EMAIL = "alice@example.com"
TOKEN = "sk-live-ABCDEF123456"

#: Similar-but-not-equal values used as false-positive controls.
CONTROLS = (
    "123-45-6780",          # one digit off
    "123-45-678",           # truncated
    "X123-45-6789Y",        # embedded, alnum boundaries
    "alice@example.org",    # different TLD
    "sk-live-ABCDEF123457", # one digit off
)


@dataclass(frozen=True)
class Annotation:
    path: str
    rule_id: str
    value: str  # the exact planted variant as it appears in the document


def dictionary_rules():
    from .core import DictRule

    return [
        DictRule("known-ssn", 3, (SSN,)),
        DictRule("known-email", 2, (EMAIL,)),
        DictRule("known-token", 1, (TOKEN,)),
    ]


def field_rules():
    from .core import FieldRule

    return [FieldRule("user-ssn-field", 5, "/users/*/ssn")]


def whitelist():
    from .core import WhitelistEntry

    return [WhitelistEntry("/archive/0/ssn", "known-ssn", 3)]


def build_reference_document():
    """Return ``(document, annotations, whitelisted, controls)``."""
    doc = {
        "users": [
            {"name": "Alice", "ssn": SSN},
            {
                "name": "Bob",
                "ssn": "000-00-0000",  # field-rule only, not in dictionary
                "profile": {"contact": {"email": "ALICE@EXAMPLE.COM"}},
            },
            {"name": "Carol", "ssn": "123-45-6780"},  # control value
        ],
        "notes": [
            "full-width digits: １２３-４５-６７８９",
            "spaced token: sk-live- ABCDEF123456",
            "zero-width email: alice@example​.com",
            "escaped ssn: \\u0031\\u0032\\u0033-45-6789",
            "url-encoded email: alice%40example.com",
            "boundary control: X123-45-6789Y",
            "short control: 123-45-678",
            "token control: sk-live-ABCDEF123457",
        ],
        "archive": [
            {"ssn": SSN, "note": "whitelisted path"},
        ],
        "deep": [[[[{"email": EMAIL}]]]],
        "meta": {"version": 1, "tags": ["a", "b"], "active": True},
    }
    annotations = [
        Annotation("/users/0/ssn", "known-ssn", SSN),
        Annotation("/users/0/ssn", "user-ssn-field", SSN),
        Annotation("/users/1/ssn", "user-ssn-field", "000-00-0000"),
        Annotation("/users/1/profile/contact/email", "known-email", "ALICE@EXAMPLE.COM"),
        Annotation("/users/2/ssn", "user-ssn-field", "123-45-6780"),
        Annotation("/notes/0", "known-ssn", "１２３-４５-６７８９"),
        Annotation("/notes/1", "known-token", "sk-live- ABCDEF123456"),
        Annotation("/notes/2", "known-email", "alice@example​.com"),
        Annotation("/notes/3", "known-ssn", "\\u0031\\u0032\\u0033-45-6789"),
        Annotation("/notes/4", "known-email", "alice%40example.com"),
        Annotation("/deep/0/0/0/0/email", "known-email", EMAIL),
    ]
    whitelisted = [Annotation("/archive/0/ssn", "known-ssn", SSN)]
    return doc, annotations, whitelisted, list(CONTROLS)


def build_stream_sample():
    """Return ``(full_text, chunks, expected)`` where every expected
    secret crosses at least one chunk boundary."""
    text = (
        "log begin; email=alice@example.com; "
        "ssn=\\u0031\\u0032\\u0033-45-6789; "
        "token=sk-live-ABCDEF123456; log end"
    )
    chunk_size = 7
    chunks = [text[i : i + chunk_size] for i in range(0, len(text), chunk_size)]
    expected = {
        "known-email": "alice@example.com",
        "known-ssn": "\\u0031\\u0032\\u0033-45-6789",
        "known-token": "sk-live-ABCDEF123456",
    }
    return text, chunks, expected
