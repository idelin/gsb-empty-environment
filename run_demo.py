"""Run sample: align the fixture channels on a common time base.

Demonstrates: declared integer+fractional delays, smooth sample-position
driven delay changes, retraction of an unconfirmed change, checkpoint /
restore, zero-fill + contamination marks, the derived memory bound, and a
final check against the whole-segment reference convolution.
"""

import fixture
import reference
from aligner import MultiChannelAligner


def main():
    inputs = fixture.make_multichannel_input()
    length = len(inputs[0])
    print(f"sample rate      : {fixture.SAMPLE_RATE} Hz (from fixture)")
    print(f"channels         : {fixture.NUM_CHANNELS} x {length} samples")
    print(f"declared delays  : {fixture.DECLARED_DELAYS} samples")
    print(f"kernel length    : {fixture.KERNEL_LENGTH} taps "
          f"(group delay {fixture.KERNEL_GROUP_DELAY})")
    print(f"tolerance        : {fixture.TOLERANCE:g}")

    aligner = MultiChannelAligner(fixture.NUM_CHANNELS, fixture.DECLARED_DELAYS)
    print(f"memory bound     : {aligner.history_bound} samples/channel "
          f"(ceil(max_delay - group) + kernel_length)")

    # smooth, sample-position-driven delay changes
    v1 = aligner.schedule_change(1, 200, 16.0, 48)
    v2 = aligner.schedule_change(2, 320, 40.0, 64)
    v3 = aligner.schedule_change(0, 400, 20.0, 32)
    aligner.retract(v3)  # tentative change withdrawn before confirmation
    print(f"delay changes    : v{v1} ch1 @200 -> 16.0 (48), "
          f"v{v2} ch2 @320 -> 40.0 (64), v{v3} retracted")

    # stream in chunks of 44 samples, with a checkpoint round-trip in the middle
    outputs = [[] for _ in range(fixture.NUM_CHANNELS)]
    zf = [[] for _ in range(fixture.NUM_CHANNELS)]
    ct = [[] for _ in range(fixture.NUM_CHANNELS)]
    pos, checkpointed = 0, False
    while pos < length:
        stop = min(pos + 44, length)
        block = aligner.process([ch[pos:stop] for ch in inputs])
        pos = stop
        if not checkpointed and pos >= length // 2:
            payload = aligner.checkpoint()
            restored = MultiChannelAligner(fixture.NUM_CHANNELS,
                                           fixture.DECLARED_DELAYS)
            restored.load_checkpoint(payload)
            aligner = restored
            checkpointed = True
            print(f"checkpoint       : restored at input {pos}, "
                  f"frontier {aligner.emit_frontier}, "
                  f"delay_version {aligner.delay_version}")
        for c in range(fixture.NUM_CHANNELS):
            outputs[c].extend(block.samples[c])
            zf[c].extend(block.zero_filled[c])
            ct[c].extend(block.contaminated[c])
    block = aligner.finish()  # tail flush
    for c in range(fixture.NUM_CHANNELS):
        outputs[c].extend(block.samples[c])
        zf[c].extend(block.zero_filled[c])
        ct[c].extend(block.contaminated[c])

    total = len(outputs[0])
    print(f"output           : {total} samples/channel on the common time base")
    print(f"common valid     : {aligner.common_valid_interval()}")
    print(f"zero-filled      : {aligner.zero_filled_samples} channel-samples")
    print(f"contaminated     : {aligner.contaminated_samples} channel-samples")
    print(f"max history used : {aligner.max_history_observed} samples/channel "
          f"(bound {aligner.history_bound})")

    # verify against the whole-segment reference convolution
    changes = [[] for _ in range(fixture.NUM_CHANNELS)]
    changes[1].append((200, 16.0, 48))
    changes[2].append((320, 40.0, 64))
    rs, rz, rc = reference.reference_run(inputs, fixture.DECLARED_DELAYS,
                                         changes_per_channel=changes)
    assert len(rs[0]) == total, "output count mismatch vs reference"
    max_err = max(abs(rs[c][n] - outputs[c][n])
                  for c in range(fixture.NUM_CHANNELS) for n in range(total))
    assert max_err <= fixture.TOLERANCE, max_err
    assert rz == zf and rc == ct, "mask mismatch vs reference"
    print(f"reference check  : max |err| = {max_err:.3g} "
          f"(<= {fixture.TOLERANCE:g}), masks identical")
    print("OK")


if __name__ == "__main__":
    main()
