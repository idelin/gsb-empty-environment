# ntfs_audit

An embeddable, zero-dependency C++17 library for offline, byte-level security
auditing of raw NTFS `$MFT` `FILE` records. It is built for forensic and disk
image scanning hosts that parse untrusted images: every access is
bounds-checked, the public entry point is `noexcept`, and hostile records
produce structured findings rather than out-of-bounds reads or crashes.

## Layout

- `include/ntfs_audit/ntfs_audit.h` — single public header, data model and API.
- `src/ntfs_audit.cpp` — self-contained implementation (no third-party code).
- `tests/mft_builder.h` — test-only serializer that hand-crafts `FILE` records.
- `tests/test_ntfs_audit.cpp` — adversarial and positive test suite.

## Integration

The library is a single translation unit plus one header. Add the header path
and compile `src/ntfs_audit.cpp` with the host, or link the `ntfs_audit`
static library target from `CMakeLists.txt`.

```cpp
#include "ntfs_audit/ntfs_audit.h"

ntfs_audit::AuditOptions options;
options.expectedRecordSize = 1024;   // custom record sizes are supported
options.volumeClusterCount = clusters; // enables run volume-bound checks

ntfs_audit::AuditResult result =
    ntfs_audit::AuditRecord(rawBytes, rawSize, options);

if (!result.ok()) {
    for (const auto& finding : result.findings) {
        // finding.severity / ntfs_audit::ToString(finding.code) / .offset
    }
}
for (const auto& stream : result.dataStreams) {
    if (stream.named) { /* alternate data stream */ }
}
```

## Security guarantees

1. **Record framing and USA fixup**
   - Requires the `FILE` magic and an exact, 512-multiple record size.
   - Validates the Update Sequence Array offset/count, matches the sequence
     number at the end of every 512-byte sector, and restores the real
     sector-end bytes on a private copy before parsing.
   - Rejects out-of-bounds or misaligned first-attribute offsets, zero-length
     attributes (infinite-loop vectors), and attributes that cross the record.

2. **Attribute and data-run parsing**
   - Parses `$STANDARD_INFORMATION`, `$FILE_NAME`, resident and non-resident
     `$DATA`, and `$REPARSE_POINT`.
   - Decodes non-resident runs with signed LCN difference encoding, including
     sign extension, signed-overflow checks, truncated/malformed headers,
     negative-LCN rejection, and optional volume-bound enforcement.
   - Detects overlapping physical cluster intervals across all streams.
   - Reports every named `$DATA` as a hidden alternate data stream (warning by
     default, hard error with `treatAdsAsError`) and rejects overlong names.

3. **Reparse point path analysis**
   - Decodes UTF-16LE `SubstituteName`/`PrintName` for
     `IO_REPARSE_TAG_SYMLINK` and `IO_REPARSE_TAG_MOUNT_POINT`.
   - Hard-flags drive-absolute targets, UNC paths, device prefixes
     (`\??\`, `\\?\`, `\\.\`) and `..` traversal components.

## Build and test

The codebase compiles cleanly with:

```
-std=c++17 -Wall -Wextra -Wconversion -Werror
```

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```

The suite hand-crafts a valid record and dedicated fixtures for fixup
tampering, hidden ADS (resident and non-resident), overlapping data runs,
negative LCNs, volume-bound violations, malformed/truncated runs, and a
malicious mount-point/symlink reparse point. It also runs cleanly under
AddressSanitizer and UndefinedBehaviorSanitizer.
