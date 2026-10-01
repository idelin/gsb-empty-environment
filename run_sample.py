"""Runnable sample: check accepted requests of the bounded protocol."""

from model import Status, replay
from protocol import RequestTracker, build_request_system


def show(title, result, system=None, start=None):
    print(f"[{title}]")
    print(f"  status             : {result.status.value}")
    print(f"  may_complete       : {result.may_complete}")
    print(f"  must_complete_fair : {result.must_complete_fair}")
    print(f"  blocked_states     : {sorted(result.blocked_states)}")
    print(f"  explored_states    : {result.explored_states}")
    print(f"  note               : {result.note}")
    if result.counterexample is not None:
        trace = " -> ".join(
            f"{s.action}=>{s.target}" for s in result.counterexample
        )
        suffix = "  (cycle repeats forever)" if result.cycle else ""
        print(f"  counterexample     : {trace}{suffix}")
        if system is not None and start is not None:
            final = replay(system, start, result.counterexample)
            print(f"  replay ok, ends in : {final}")
    print()


def main():
    # 1. Healthy protocol, fairness on: completion guaranteed on all fair paths.
    system = build_request_system()
    tracker = RequestTracker(system)
    h1 = tracker.accept("req-1")
    tracker.apply(h1, "dispatch")
    show("req-1 after dispatch, fair respond", tracker.check(h1), system, tracker.state_of(h1))

    # 2. Same model, fairness off: a success path exists but is not guaranteed.
    unfair = build_request_system(fair_respond=False)
    tracker2 = RequestTracker(unfair)
    h2 = tracker2.accept("req-2")
    tracker2.apply(h2, "dispatch")
    show("req-2 after dispatch, NO fairness", tracker2.check(h2), unfair, tracker2.state_of(h2))

    # 3. Faulty variant with a non-terminal dead end: BLOCKED.
    lossy = build_request_system(include_loss=True)
    tracker3 = RequestTracker(lossy)
    h3 = tracker3.accept("req-3")
    tracker3.apply(h3, "dispatch")
    show("req-3 after dispatch, lossy model", tracker3.check(h3), lossy, tracker3.state_of(h3))

    # 4. Duplicate request IDs: independent state per acceptance.
    tracker4 = RequestTracker(build_request_system())
    a = tracker4.accept("req-4")
    b = tracker4.accept("req-4")  # same ID, independent instance
    tracker4.apply(a, "cancel")
    tracker4.apply(b, "dispatch")
    print("[duplicate request IDs]")
    print(f"  {a} state={tracker4.state_of(a)} complete={tracker4.is_complete(a)}")
    print(f"  {b} state={tracker4.state_of(b)} complete={tracker4.is_complete(b)}")
    print(f"  {b} check={tracker4.check(b).status.value} (cancel is a terminal state)")
    print()

    # 5. Bounded search: tiny bound leaves the verdict pending.
    tight = RequestTracker(build_request_system(), max_states=1)
    h5 = tight.accept("req-5")
    show("req-5 with max_states=1", tight.check(h5))


if __name__ == "__main__":
    main()
