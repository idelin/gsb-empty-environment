"""Runnable sample for the pseudonymizer package.

All subjects and keys below are synthetic. Run:  python3 demo.py
"""

from __future__ import annotations

import threading

from pseudonymizer import AuditLog, KeyStore, OperationCancelled, PseudonymStore

KEY_V1 = b"demo-synthetic-key-v1-0123456789abcd"
KEY_V2 = b"demo-synthetic-key-v2-0123456789abcd"


def colliding_derive(key, domain, subject, counter):
    """Injected collision oracle: every first attempt returns a constant."""
    if counter == 0:
        return "c" * 32
    from pseudonymizer import derive_pseudonym

    return derive_pseudonym(key, domain, subject, counter=counter)


def main() -> None:
    audit = AuditLog()
    keystore = KeyStore()
    keystore.add("v1", KEY_V1)
    store = PseudonymStore(keystore, audit=audit)

    print("== 1. stable per (domain, subject), separated across domains ==")
    pid_a1 = store.get_or_create("domain-a", "subject-001")
    pid_a2 = store.get_or_create("domain-a", "subject-001")
    pid_b1 = store.get_or_create("domain-b", "subject-001")
    print(f"domain-a subject-001: {pid_a1}")
    print(f"repeat (stable):      {pid_a2}  stable={pid_a1 == pid_a2}")
    print(f"domain-b subject-001: {pid_b1}  separated={pid_a1 != pid_b1}")

    print("\n== 2. cross-table references ==")
    store.add_reference("domain-a", pid_a1, "record-0001")
    store.add_reference("domain-a", pid_a1, "record-0002")
    print(f"refs on {pid_a1}: {sorted(store.state.refs[('domain-a', pid_a1)])}")

    print("\n== 3. atomic key rotation with explicit old->new links ==")
    store.rotate("v2", KEY_V2)
    new_pid = store.get_or_create("domain-a", "subject-001")
    print(f"old pid (v1): {pid_a1}")
    print(f"new pid (v2): {new_pid}")
    print(f"resolve_link(old) == new: {store.resolve_link('domain-a', pid_a1) == new_pid}")
    print(f"refs migrated: {sorted(store.state.refs[('domain-a', new_pid)])}")
    print(f"consistency after rotation: {store.check_consistency()}")

    print("\n== 4. injected collision -> deterministic resolution ==")
    ks2 = KeyStore()
    ks2.add("v1", KEY_V1)
    colliding = PseudonymStore(ks2, audit=audit, derive_fn=colliding_derive)
    c1 = colliding.get_or_create("domain-x", "subject-001")
    c2 = colliding.get_or_create("domain-x", "subject-002")
    print(f"subject-001 -> {c1} (counter 0)")
    print(f"subject-002 -> {c2} (counter "
          f"{colliding.state.counters[('domain-x', 'subject-002')]})")
    print(f"distinct: {c1 != c2}, consistent: {colliding.check_consistency()}")

    print("\n== 5. cancellation leaves the store unchanged ==")
    cancel = threading.Event()
    cancel.set()
    try:
        store.get_or_create("domain-a", "subject-999", cancel_event=cancel)
    except OperationCancelled as exc:
        print(f"cancelled as expected: {exc}")
    print(f"assignments unchanged: {('domain-a', 'subject-999') not in store.state.assignments}")

    print("\n== 6. concurrent same-subject requests agree ==")
    results = []
    threads = [
        threading.Thread(
            target=lambda: results.append(
                store.get_or_create("domain-a", "subject-777")
            )
        )
        for _ in range(16)
    ]
    for t in threads:
        t.start()
    for t in threads:
        t.join()
    print(f"16 threads -> {len(set(results))} distinct pseudonym(s)")

    print("\n== 7. recovery check and audit log (pseudonyms only) ==")
    print(f"final consistency check: {store.check_consistency()}")
    print(audit.dump())


if __name__ == "__main__":
    main()
