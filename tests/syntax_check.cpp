#include "kpti/audit.hpp"

int main() {
  kpti::FlatPhysicalMemory memory(4096);
  kpti::Auditor auditor(memory);
  kpti::AuditParams params;
  kpti::AuditReport report = auditor.run(params);
  return report.ok() ? 0 : 1;
}
