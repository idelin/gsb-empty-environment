"""Sample run: shrink a wallet-model counterexample to a local minimum."""

from shrinker import replay, shrink
from wallet_model import WalletModel, initial_counterexample


def show(label, config, trace, faults):
    print(f"--- {label} ---")
    print(f"  config: {config}")
    print(f"  trace : {trace}")
    print(f"  faults: {sorted(faults)}")


def main():
    model = WalletModel()
    config, trace, faults = initial_counterexample()

    show("Original counterexample", config, trace, faults)
    before = replay(model, config, trace, faults)
    print(
        f"  replay: outcome={before.outcome} "
        f"signature={before.signature} step={before.step}"
    )
    print()

    result = shrink(model, config, trace, faults, max_replays=500)
    show("Shrunk counterexample", result.config, result.trace, result.faults)
    check = replay(model, result.config, result.trace, result.faults)
    print(
        f"  verification replay: outcome={check.outcome} "
        f"signature={check.signature} step={check.step}"
    )
    print(f"  status: {result.status} (replays used: {result.replays_used})")
    assert check.outcome == "violation" and check.signature == before.signature
    print()

    tight = shrink(model, config, trace, faults, max_replays=8)
    print("--- Same search with a tight replay budget (max_replays=8) ---")
    print(f"  status: {tight.status} (replays used: {tight.replays_used})")
    recheck = replay(model, tight.config, tight.trace, tight.faults)
    print(
        f"  partial result still replays: outcome={recheck.outcome} "
        f"signature={recheck.signature}"
    )


if __name__ == "__main__":
    main()
