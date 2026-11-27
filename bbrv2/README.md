# bbrv2

`package bbrv2` is a dependency-free Go implementation of a BBRv2-style
model-based congestion controller. It jointly estimates the bottleneck
bandwidth (`BtlBw`), the minimum propagation delay (`minRTT`) and a safe
in-flight ceiling (`inflight_hi`), and exposes smoothed pacing-rate and
congestion-window outputs for a transport's ACK processing path.

## Model and state machine

- **Windowed max filter** — `maxfilter.go` is a faithful Go port of the
  Linux BBR `bbr_minmax` three-slot Kathleen Nichols windowed-max filter.
  The window length is expressed in Round-Trip rounds; the maximum always
  lives in slot 0 and stale values age out at round boundaries.
- **Round-Trip accounting** — a round closes once all bytes outstanding at
  the previous boundary have been delivered (`totalDelivered` reaches
  `nextRoundDelivered`). Per-round aggregates (delivered/lost/ECN bytes,
  app-limited flag, peak in-flight and peak raw rate) are consumed at the
  boundary and reset for the next round.
- **Four top-level states** — `STARTUP`, `DRAIN`, `PROBE_BW`, `PROBE_RTT`.
- **Four PROBE_BW sub-phases** — `DOWN`, `CRUISE`, `REFILL`, `UP`.

## The four coupling invariants

1. **app_limited samples.** An application-limited rate sample never lowers
   `BtlBw` and never counts toward STARTUP full-pipe detection, but an
   app-limited sample above the current windowed maximum is still adopted.
2. **STARTUP exit.** STARTUP leaves for DRAIN only after `FullBwRounds`
   (default 3) consecutive non-app-limited rounds whose peak delivery rate
   grows by less than `StartupGrowthFactor` (default 1.25), or immediately on
   a round whose loss/ECN ratio crosses its threshold. DRAIN drains until
   in-flight falls to the BDP before entering PROBE_BW.
3. **PROBE_RTT.** When `minRTT` is stale for `MinRTTExpiry` (10s), the cwnd
   is capped at `min(cwnd, max(4*MSS, 0.5*BDP))`. The dwell timer starts only
   after real in-flight bytes drop below that target and runs for at least
   `max(ProbeRTTMinDuration, minRTT)` (default 200ms floor).
4. **inflight_hi adaptation.** A loss/ECN round shrinks `inflight_hi` to the
   peak in-flight level that experienced congestion, floored at one BDP, and
   DOWN/CRUISE cruise at `HeadroomFactor * inflight_hi` (default 0.85). UP
   probing doubles its extra in-flight allowance every round until a plateau
   or congestion ends the probe.

Rate samples containing any loss/ECN signal are excluded from the `BtlBw`
max filter for the whole round, since queue draining overstates the safe
delivery rate.

## Usage

```go
c := bbrv2.New(bbrv2.Config{}) // zero fields take documented defaults

// When handing bytes to the network:
c.OnSent(n)

// On each ACK, feed a delivery sample:
c.OnSample(bbrv2.RateSample{
    Now:            now,
    RTT:            rtt,
    BytesDelivered: delivered,
    BytesAcked:     acked,
    BytesLost:      lost,
    BytesECN:       ecn,
    Interval:       interval,
})

// Outputs for the transport:
rate := c.PacingRate() // bytes/sec, smoothed
win  := c.Cwnd()       // bytes
```

When the application cannot fill the pipe, call `SetAppLimited()`; the flag
is cleared automatically once in-flight reaches the congestion window.

All tunables are exposed on `Config`; see `bbr.go` for the constants and
their defaults.
