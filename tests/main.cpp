#include "test_util.hpp"

#include <iostream>

void test_endpoint_sweep_sparse();
void test_cross_leaf_partial_authority();
void test_same_bytes_different_versions();
void test_alias_duplicate_votes();
void test_nested_intervals();
void test_minimality_tie_breaks();
void test_revocation_and_old_lease_readback();
void test_threshold_change_snapshot_isolation();
void test_host_hooks_injection();
void test_candidate_cap_and_duplicates();
void test_certificate_tamper_detection();
void test_exhaustive_subset_oracle();

int main() {
    test_endpoint_sweep_sparse();
    test_cross_leaf_partial_authority();
    test_same_bytes_different_versions();
    test_alias_duplicate_votes();
    test_nested_intervals();
    test_minimality_tie_breaks();
    test_revocation_and_old_lease_readback();
    test_threshold_change_snapshot_isolation();
    test_host_hooks_injection();
    test_candidate_cap_and_duplicates();
    test_certificate_tamper_detection();
    test_exhaustive_subset_oracle();

    if (testutil::failures() != 0) {
        std::cerr << testutil::failures() << " test(s) failed\n";
        return 1;
    }
    std::cout << "all reassembly tests passed\n";
    return 0;
}
