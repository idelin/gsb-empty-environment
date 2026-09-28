"""Runnable demo: certified-envelope rejection sampling on a finite domain."""

from rejection_sampling import (
    BoundViolationError,
    CancellationToken,
    Certificate,
    Envelope,
    Partition,
    RejectionSampler,
    Status,
    UniformFiniteProposal,
    analyze_finite,
    empirical_report,
    recover_confirmed_samples,
    total_variation,
    verify_envelope_exactly,
)

DOMAIN = list(range(10))
WEIGHTS = [1.0, 2.0, 3.0, 4.0, 5.0, 4.0, 3.0, 2.0, 1.0, 0.5]
target = lambda x: WEIGHTS[x]
proposal = UniformFiniteProposal(DOMAIN)

print("=" * 72)
print("1. Per-partition envelope: one certified, one exactly verified")
print("=" * 72)
envelope = Envelope(
    [
        Partition(
            name="low",
            contains=lambda x: x < 5,
            bound=50.0,
            certificate=Certificate("caller proved max ratio on {0..4} is 50"),
        ),
        Partition(name="high", contains=lambda x: x >= 5, bound=40.0),  # no cert yet
    ]
)
report = verify_envelope_exactly(envelope, target, proposal, DOMAIN)
print(f"[exact] envelope verification: {report.per_partition}, all_ok={report.all_ok}")

print()
print("=" * 72)
print("2. Exact analysis vs empirical run (labelled separately)")
print("=" * 72)
exact = analyze_finite(DOMAIN, target, proposal, envelope)
print(f"[exact]     acceptance probability : {exact.acceptance_probability:.6f}")
print(f"[exact]     expected attempts/sample: {exact.expected_attempts_per_sample:.4f}")
print(f"[exact]     expected rejections/sample: {exact.expected_rejections_per_sample:.4f}")

sampler = RejectionSampler(target, proposal, envelope, seed=20260928)
result = sampler.sample(5000)
emp = empirical_report(result)
print(f"[empirical] seed={result.seed}, samples={emp.samples}, attempts={emp.attempts}, "
      f"rejections={emp.rejections}")
print(f"[empirical] acceptance rate (accepted/attempts): {emp.acceptance_rate:.6f}")
tv = total_variation(exact.output_distribution, emp.distribution)
print(f"[compare]   total variation exact-vs-empirical: {tv:.4f}")
print("            x : exact_p   empirical_p")
for x in DOMAIN:
    print(f"            {x} : {exact.output_distribution[x]:.4f}    "
          f"{emp.distribution.get(x, 0.0):.4f}")

print()
print("=" * 72)
print("3. Recovery: replay seed to restore confirmed sample positions")
print("=" * 72)
small = RejectionSampler(target, proposal, envelope, seed=7).sample(6)
print(f"records: {[(r.value, r.attempt_index) for r in small.records]}")
recovered = recover_confirmed_samples(target, proposal, envelope, small.seed, small.records)
print(f"recovered values: {recovered}")
assert recovered == list(small.samples)
print("recovery matches original samples: OK")

print()
print("=" * 72)
print("4. Budget exhaustion and cancellation return partial results")
print("=" * 72)
tight = RejectionSampler(target, proposal, envelope, seed=11).sample(1000, max_attempts=12)
print(f"budget: status={tight.status.value}, sampled={tight.accepted}, "
      f"remaining={tight.remaining}, attempts={tight.attempts}")
token = CancellationToken()
token.cancel()
cancelled = RejectionSampler(target, proposal, envelope, seed=11).sample(1000, token=token)
print(f"cancel: status={cancelled.status.value}, sampled={cancelled.accepted}, "
      f"remaining={cancelled.remaining}")

print()
print("=" * 72)
print("5. Envelope violation aborts (no clamping to 1)")
print("=" * 72)
lying = Envelope(
    [
        Partition(
            name="all",
            contains=lambda x: True,
            bound=20.0,  # true max ratio is 50: certificate is wrong
            certificate=Certificate("unverified caller claim"),
        )
    ]
)
try:
    RejectionSampler(target, proposal, lying, seed=0).sample(1000)
except BoundViolationError as exc:
    print(f"BoundViolationError: {exc}")

print()
print("=" * 72)
print("6. Adaptive refinement tightens the global constant")
print("=" * 72)
loose = Envelope(
    [
        Partition(
            name="all",
            contains=lambda x: True,
            bound=80.0,
            certificate=Certificate("loose but valid bound"),
        )
    ]
)
before = analyze_finite(DOMAIN, target, proposal, loose)
loose.refine(
    "all",
    [
        Partition(name="low", contains=lambda x: x < 5, bound=50.0),
        Partition(name="high", contains=lambda x: x >= 5, bound=40.0),
    ],
    target=target,
    proposal=proposal,
    domain=DOMAIN,
)
after = analyze_finite(DOMAIN, target, proposal, loose)
print(f"[exact] acceptance before refine: {before.acceptance_probability:.6f}")
print(f"[exact] acceptance after  refine: {after.acceptance_probability:.6f}")
print(f"[exact] partitions now verified: "
      f"{[(p.name, p.verified_exactly) for p in loose.partitions]}")
