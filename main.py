"""End-to-end demo of the revisable alert state machine.

Scenario: enter threshold 10, duration 5.  Samples 11@0 and 12@6 fire an
alert at event time 5; a later backfill of 8@3 proves the duration was
never sustained, so the already-sent alert is revoked (not deleted).
"""

from alerting import AlertSystem, RecordingTransport, RuleVersion


def show(title, sys):
    print(f"\n== {title} (revision {sys.revision}) ==")
    for rec in sys.audit:
        print(f"  rev={rec.revision} {rec.kind.value:<8} {rec.alert_id} {rec.payload}")


def main():
    sys = AlertSystem()
    sys.set_rule(RuleVersion("cpu", version=1,
                             enter_threshold=10, exit_threshold=9,
                             enter_duration=5))
    transport = RecordingTransport()

    sys.ingest(0, 11)
    sys.ingest(6, 12)
    sys.recompute()
    sys.outbox.dispatch(transport)
    for entry in sys.outbox.entries:
        sys.outbox.confirm(entry.entry_id)
    show("alert fired at t=5 and confirmed", sys)

    # Historical data arrives late: the condition broke at t=3.
    sys.ingest(3, 8)
    emitted = sys.recompute()
    sys.outbox.dispatch(transport)
    show("backfill 8@3 -> original alert revoked", sys)
    for entry in emitted:
        print(f"  correction: {entry.kind.value} {entry.alert_id} rev={entry.revision}")

    print("\nDelivered to receiver (idempotent):", transport.delivered)


if __name__ == "__main__":
    main()
