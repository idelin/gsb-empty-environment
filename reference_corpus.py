"""Synthetic reference corpus with independent hand-written annotations.

All values below are synthetic and were invented for this test corpus.
``EXPECTED`` was annotated by hand, independently of the scanner
implementation, and is used to check for missed detections (recall),
duplicate evidence, and false positives (everything under ``/meta`` is a
near-miss control that must NOT match).
"""
from sensiscan.scanner import Rule, WhitelistEntry

# --- Rule definitions (parameters of the experiment) ---------------------
RULES = [
    Rule(
        rule_id="known-email",
        version="v1.0",
        fields=("*",),
        entries=("alice.w@example.com", "carol@example.net"),
    ),
    Rule(
        rule_id="known-phone",
        version="v1.0",
        fields=("phone", "phones"),
        entries=("13800000000",),
    ),
    Rule(
        rule_id="known-name",
        version="v1.0",
        fields=("name", "full_name"),
        entries=("alice zhang",),
    ),
]

# --- Whitelist: exact path + exact rule version --------------------------
# "/public_email" is exempted for known-email v1.0 only.
# "/legacy/email" carries a STALE version (v0.9) and must NOT exempt.
WHITELIST = [
    WhitelistEntry("/public_email", "known-email", "v1.0"),
    WhitelistEntry("/legacy/email", "known-email", "v0.9"),
]

# --- Synthetic document --------------------------------------------------
DOC = {
    "user": {
        # full-width + uppercase variant of alice.w@example.com
        "email": "ＡＬＩＣＥ．Ｗ＠ｅｘａｍｐｌｅ．ｃｏｍ",
        # \u0041 escape + double space -> "alice zhang" via desep path
        "name": "\\u0041lice  Zhang",
        # zero-width space inside the name
        "full_name": "Alice\u200b Zhang",
        # nested arrays: separator variant and +86-prefixed variant
        "phones": [["138-0000-0000"], ["+86 138 0000 0000"]],
    },
    # percent-encoding and HTML numeric reference variants, one string
    "notes": "reach alice.w%40example.com or &#97;lice.w@example.com today",
    # whitelisted exact path -> suppressed
    "public_email": "alice.w@example.com",
    # same key name "email", different path -> NOT exempted
    "backup": {"email": "alice.w@example.com"},
    # whitelist entry exists but rule version differs -> NOT exempted
    "legacy": {"email": "alice.w@example.com"},
    # near-miss controls: similar but NOT equal to any dictionary entry
    "meta": {
        "email": "bob@example.com",
        "website": "alice.w@example.org",
        "phone": "13800000001",
        "name": "alice zhou",
        "id": 424242,
    },
}

# --- Independent hand-written annotations --------------------------------
EXPECTED = [
    {"path": "/user/email", "rule_id": "known-email", "count": 1},
    {"path": "/user/name", "rule_id": "known-name", "count": 1},
    {"path": "/user/full_name", "rule_id": "known-name", "count": 1},
    {"path": "/user/phones/0/0", "rule_id": "known-phone", "count": 1},
    {"path": "/user/phones/1/0", "rule_id": "known-phone", "count": 1},
    {"path": "/notes", "rule_id": "known-email", "count": 2},
    {"path": "/backup/email", "rule_id": "known-email", "count": 1},
    {"path": "/legacy/email", "rule_id": "known-email", "count": 1},
]

EXPECTED_SUPPRESSED = 1  # /public_email via known-email v1.0

# Paths under which NO evidence may appear (false-positive controls).
CONTROL_PREFIXES = ("/meta",)

# Raw sensitive values that must never appear in the serialized report.
SECRETS = [
    "alice.w@example.com",
    "carol@example.net",
    "13800000000",
    "alice zhang",
    "Alice  Zhang",
    "Alice\u200b Zhang",
    "alice.w%40example.com",
    "&#97;lice.w@example.com",
    "ＡＬＩＣＥ．Ｗ＠ｅｘａｍｐｌｅ．ｃｏｍ",
]
