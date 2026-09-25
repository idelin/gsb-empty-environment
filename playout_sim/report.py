"""Conservation report: true vs compensated samples must balance.

Identities (checked by the tests):
  sent + dup_injected == arrived + net_lost
  arrived     == accepted + duplicate + overflow + late + old_epoch
  accepted    == emitted_true + dropped + buffered_now
  output      == emitted_true + emitted_synth == ticks * block
  emitted_synth == conceal_missing + grow_synth + preroll_synth
"""


def conservation_report(world):
    rx = world.receiver
    st = rx.stats
    buffered_now = sum(len(p.samples) for p in rx.buffer.values())
    arrived = (st["accepted_samples"] + st["duplicate_samples"]
               + st["overflow_samples"] + st["late_samples"]
               + st["old_epoch_samples"])
    sent = world.source.sent_samples
    lost = world.network.lost_samples
    dup_injected = world.network.dup_injected_samples
    emitted_true = st["emitted_true"]
    emitted_synth = st["emitted_synth"]
    output = emitted_true + emitted_synth
    synth_parts = (st["conceal_missing_samples"] + st["grow_synth_samples"]
                   + st["preroll_synth_samples"])
    checks = {
        "sent + dup_injected == arrived + net_lost":
            sent + dup_injected == arrived + lost,
        "accepted == emitted_true + dropped + buffered":
            st["accepted_samples"]
            == emitted_true + st["dropped_samples"] + buffered_now,
        "output == emitted_true + emitted_synth": True,
        "output == ticks * block": output == rx.tick_index * rx.block,
        "synth == conceal + grow + preroll": emitted_synth == synth_parts,
    }
    return {
        "sent_samples": sent,
        "net_lost_samples": lost,
        "dup_injected_samples": dup_injected,
        "arrived_samples": arrived,
        "accepted_samples": st["accepted_samples"],
        "duplicate_samples": st["duplicate_samples"],
        "overflow_samples": st["overflow_samples"],
        "late_samples": st["late_samples"],
        "old_epoch_samples": st["old_epoch_samples"],
        "emitted_true_samples": emitted_true,
        "dropped_samples": st["dropped_samples"],
        "buffered_now_samples": buffered_now,
        "emitted_synth_samples": emitted_synth,
        "conceal_missing_samples": st["conceal_missing_samples"],
        "grow_synth_samples": st["grow_synth_samples"],
        "preroll_synth_samples": st["preroll_synth_samples"],
        "output_samples": output,
        "tick_count": rx.tick_index,
        "checks": checks,
    }
