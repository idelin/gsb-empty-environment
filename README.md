# netsec — exec transition policy

`netsec` is a C++20 library for host security modules that make their final
decision immediately before `exec`.  It evaluates a domain transition against
the current domain, the executable's file label, the requested target domain,
and the rules that were declared for exactly that tuple.

The central invariant is monotonicity: a transition may restrict
permissions, but it may never add file, network, or capability permissions.
Every exec also re-evaluates the artifacts that survive the transition
(descriptors, environment, and namespaces).  Any failed re-evaluation rejects
the exec.

## Security rules

- The `(current_domain, file_label, target_domain)` tuple must match an enabled
  `TransitionRule`; there is no wildcard or implicit transition between
  domains.
- Target file rules must be a subset of the source domain's rules for every
  file label.  Target network permissions and capability names must also be
  subsets of the source domain.
- A permissive file label, a writable executable path, a writable executable
  file, a symlink, or a file with more than one hard link is not a legal
  transition entry point.
- Inherited descriptors must be allowed by the target domain's descriptor
  policy.  Descriptors marked close-on-exec are not inherited.
- Inherited environment names must satisfy the target's environment policy.
  Loader/shell injection variables such as `LD_PRELOAD` and `DYLD_*` are always
  rejected.
- Inherited namespaces must satisfy the target's namespace allow-list and
  required-namespace constraints.
- The source domain's capability ceiling is the current capability set unless
  the caller supplies one.  The effective result is always the intersection
  with the target domain, so a transition can only drop capabilities.
- Policy mutations are atomic and bump a generation number.  Evaluation uses
  a single snapshot, and the generation is recorded in the decision.

`inspect_executable()` fills the executable metadata from the filesystem,
including a conservative writable-ancestor check and `st_nlink`.  It is a
preflight helper; the host module must authorize and execute the same opened
file object (for example, with `fexecve`) so that the path cannot be replaced
between the check and the exec.

`ExecRequest` must contain the complete set of descriptors, environment
variables, and namespaces that will survive the exec.  The component checks
what the host reports; it cannot discover omitted inherited state.

## API

```cpp
#include "netsec.hpp"

netsec::DomainPolicy source;
source.name = "service";
source.file_rules = {{"service-bin", netsec::FileAccess::Read |
                                         netsec::FileAccess::Execute}};
source.network_access = netsec::NetworkAccess::Connect;
source.capabilities = {"CAP_NET_RAW"};
source.default_inherited_descriptor_access = netsec::FileAccess::Read;

netsec::DomainPolicy target;
target.name = "worker";
target.file_rules = {{"service-bin", netsec::FileAccess::Read}};
target.network_access = netsec::NetworkAccess::None;
target.capabilities = {};

netsec::TransitionPolicy policy;
policy.add_domain(source)
      .add_domain(target)
      .add_transition({"service", "service-bin", "worker", true});

netsec::ExecutableInfo file;
file.path = "/usr/libexec/service";
file.label = "service-bin";
// In production, take these fields from inspect_executable() on the opened
// file object the host will actually exec.
netsec::ExecRequest request{"service", "worker", file};
request.environment = {{"HOME", "/home/user"}};
request.namespaces = {"net"};

const netsec::TransitionResult decision = policy.evaluate(request);
if (!decision.allowed) {
  // Reject exec and report decision.code / decision.reason.
}
```

`TransitionResult` exposes `allowed`, `code`, `reason`,
`effective_capabilities`, `effective_network_access`, and
`policy_generation`.  `validate()` reports transitions that are undeclared,
disabled, permissive, or widening so a policy loader can reject a bad update
before installing it.  `apply_update(PolicyUpdate)` installs a group of
domain/rule changes under one lock and one generation bump; `is_current()`
lets the host detect that the policy changed between authorization and the
actual exec.

## Build and test

```sh
cmake -S . -B build -DCMAKE_CXX_STANDARD=20
cmake --build build
ctest --test-dir build --output-on-failure
```

The regression suite covers legitimate transitions, permissive labels,
writable entry points, hard links, inherited descriptors, environment and
namespace re-evaluation, and live rule updates.
