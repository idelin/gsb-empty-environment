// The public header must be self-contained and compile cleanly under the
// project's strict warning set as a single translation unit.
#include "ffi_safety/ffi_safety.hpp"

int main() {
  ffi_safety::Monitor monitor;
  if (monitor.acquire_owned(1) != ffi_safety::Verdict::Ok) {
    return 1;
  }
  ffi_safety::Handle loan = monitor.borrow_mut(1);
  if (!loan.valid()) {
    return 2;
  }
  if (monitor.deref(loan) != ffi_safety::Verdict::Ok) {
    return 3;
  }

  ffi_safety::AdversarialSearcher searcher;
  ffi_safety::SearchResult result = searcher.search_dangling_or_alias();
  return result.found ? 0 : 4;
}
