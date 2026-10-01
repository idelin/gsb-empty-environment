"""End-to-end demo of the pseudonym service. Synthetic data only.

Run: python3.11 demo.py
"""
from __future__ import annotations

import os
import tempfile
import threading

from pseudonym_service import AuditLog, CancelledError, KeyRing, PseudonymService

KEY_V1 = b"demo-synthetic-key-v1-0000000000"
KEY_V2 = b"demo-synthetic-key-v2-0000000000"


def main() -> None:
    audit = AuditLog()
    service = PseudonymService(KeyRing(KEY_V1), audit=audit)

    print("== 1. Issuance: stable per domain, isolated across domains ==")
    for domain in ("billing", "analytics"):
        pseudonym = service.get_pseudonym(domain, "subject-000")
        again = service.get_pseudonym(domain, "subject-000")
        print(f"  {domain:9s} -> {pseudonym}  (stable: {pseudonym == again})")

    print("\n== 2. Injected collision -> deterministic resolution ==")
    hook = lambda domain, subject, epoch: subject == "subject-001" and epoch == 0
    colliding = PseudonymService(KeyRing(KEY_V1), collision_hook=hook)
    pseudonym = colliding.get_pseudonym("billing", "subject-001")
    record = colliding.resolve(pseudonym)
    print(f"  resolved at epoch={record.epoch}: {pseudonym}")
    replay = PseudonymService(KeyRing(KEY_V1), collision_hook=hook)
    print(f"  deterministic replay matches: {replay.get_pseudonym('billing', 'subject-001') == pseudonym}")

    print("\n== 3. Key rotation: atomic publish + explicit old->new mapping ==")
    for i in range(5):
        service.get_pseudonym("billing", f"subject-{i:03d}")
    old = service.get_pseudonym("billing", "subject-000")
    new_version = service.rotate_key(KEY_V2)
    new = service.get_pseudonym("billing", "subject-000")
    print(f"  active version: {new_version}")
    print(f"  old: {old}\n  new: {new}")
    print(f"  mapping[old] == new: {service.rotation_map()[old] == new}")
    print(f"  old reference still resolves: {service.resolve(old).subject == 'subject-000'}")
    print(f"  resolve_current(old).version: {service.resolve_current(old).key_version}")

    print("\n== 4. Cancelled rotation leaves state untouched ==")
    def abort():
        raise CancelledError("operator abort")
    try:
        service.rotate_key(b"demo-synthetic-key-v3-0000000000", cancel_check=abort)
    except CancelledError as exc:
        print(f"  cancelled: {exc}; active version still {service.active_version}")

    print("\n== 5. Concurrent same-subject requests are consistent ==")
    results: list[str] = []
    lock = threading.Lock()
    def worker():
        value = service.get_pseudonym("billing", "subject-000")
        with lock:
            results.append(value)
    threads = [threading.Thread(target=worker) for _ in range(16)]
    for t in threads:
        t.start()
    for t in threads:
        t.join()
    print(f"  16 threads -> {len(set(results))} distinct pseudonym(s)")

    print("\n== 6. Persistence & recovery consistency ==")
    with tempfile.TemporaryDirectory() as tmp:
        path = os.path.join(tmp, "state.json")
        service.save(path)
        keyring = KeyRing(KEY_V1)
        keyring.add_version(KEY_V2, 2)
        keyring.activate(2)
        restored = PseudonymService.load(path, keyring)
        restored.verify_consistency()
        blob = open(path, encoding="utf-8").read()
        print(f"  recovered resolve_current matches: "
              f"{restored.resolve_current(old).pseudonym == new}")
        print(f"  key material in saved file: "
              f"{KEY_V1.decode() in blob or KEY_V2.decode() in blob}")

    print("\n== 7. Audit log (pseudonyms only, no subjects, no keys) ==")
    for entry in audit.entries[:6]:
        print(f"  {entry.render()}")
    forbidden = [f"subject-{i:03d}" for i in range(5)] + [KEY_V1.decode(), KEY_V2.decode()]
    print(f"  forbidden tokens found in audit log: {len(audit.scan_forbidden(forbidden))}")


if __name__ == "__main__":
    main()
