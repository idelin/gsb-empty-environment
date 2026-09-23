"""Demo: schema versions take effect at commit sequence; late old events
are still parsed with their own version; unknown schemas are buffered.

Run: python3 run_demo.py
"""

from pathlib import Path

from cdc import ChangeParser, DELETED, SchemaRegistry, UnknownFieldError
from cdc.fixtures import load_changes, load_schema_versions

FIXTURES = Path(__file__).parent / "fixtures"


def render(parsed, registry):
    version = registry.resolve(parsed.table, parsed.commit_seq)
    names = {f.stable_id: f.name for f in version.fields}

    def show(image):
        parts = []
        for sid, value in image.items():
            text = "DELETED" if value is DELETED else repr(value)
            parts.append(f"{names[sid]}#{sid}={text}")
        return "{" + ", ".join(parts) + "}"

    print(
        f"  {parsed.txn_id} commit_seq={parsed.commit_seq} "
        f"-> parsed with schema v{parsed.schema_version}"
    )
    print(f"    before: {show(parsed.before)}")
    print(f"    after:  {show(parsed.after)}")


def main():
    schemas = load_schema_versions(FIXTURES / "schema_changes.json")
    changes = load_changes(FIXTURES / "changes.json")
    v1 = next(s for s in schemas if s.version == 1)
    v2 = next(s for s in schemas if s.version == 2)

    registry = SchemaRegistry()
    parser = ChangeParser(registry, buffer_capacity=8)

    print("== 1. Only schema v2 (effective_from=100) is known so far ==")
    parser.register_schema(v2)

    print("== 2. New event (commit_seq=120) parses immediately with v2 ==")
    parsed = parser.submit(changes[0])
    render(parsed, registry)
    print("  note: display_name=null stays None (explicit null)\n")

    print("== 3. Late old events (commit_seq 40, 10) have no covering schema ==")
    for raw in changes[1:]:
        result = parser.submit(raw)
        print(f"  {raw['txn_id']} commit_seq={raw['commit_seq']} -> "
              f"buffered (buffered={len(parser.buffer)})")
        assert result is None

    print("\n== 4. Schema v1 arrives late; buffered events drain and parse "
          "with v1, NOT v2 ==")
    drained = parser.register_schema(v1)
    for parsed in drained:
        render(parsed, registry)
    print("  note: user_name maps to stable id 2, same as display_name;")
    print("        email missing from after-image is DELETED, not null\n")

    print("== 5. Unknown field names are rejected, never guessed ==")
    bad = {
        "txn_id": "txn-200",
        "table": "users",
        "pk": {"user_id": 1},
        "commit_seq": 130,
        "before": {},
        "after": {"user_id": 1, "nickname": "oops"},
    }
    try:
        parser.submit(bad)
    except UnknownFieldError as exc:
        print(f"  rejected: {exc}")

    print("\n== 6. Buffer is bounded: overflow evicts the oldest entry ==")
    tight = ChangeParser(SchemaRegistry(), buffer_capacity=2)
    for i in range(3):
        tight.submit({
            "txn_id": f"txn-x{i}", "table": "ghost", "pk": i,
            "commit_seq": i, "before": {}, "after": {},
        })
    print(f"  submitted 3, capacity 2 -> buffered={len(tight.buffer)}, "
          f"evicted={tight.buffer.evicted}")


if __name__ == "__main__":
    main()
