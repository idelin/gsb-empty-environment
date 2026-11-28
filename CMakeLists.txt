cmake_minimum_required(VERSION 3.16)
project(w28_11965fc718 CXX)
set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)

# The library targets embedded/forensic hosts: no exceptions escaping the
# public API, no external dependencies, and every warning is fatal.
add_library(ntfs_audit STATIC
    src/ntfs_audit.cpp
)
target_include_directories(ntfs_audit PUBLIC
    ${CMAKE_CURRENT_SOURCE_DIR}/include
)
target_compile_options(ntfs_audit PRIVATE
    -Wall
    -Wextra
    -Wconversion
    -Werror
)

enable_testing()
add_executable(ntfs_audit_tests
    tests/test_ntfs_audit.cpp
)
target_link_libraries(ntfs_audit_tests PRIVATE ntfs_audit)
target_include_directories(ntfs_audit_tests PRIVATE
    ${CMAKE_CURRENT_SOURCE_DIR}/tests
)
target_compile_options(ntfs_audit_tests PRIVATE
    -Wall
    -Wextra
    -Wconversion
    -Werror
)
add_test(NAME ntfs_audit_tests COMMAND ntfs_audit_tests)
