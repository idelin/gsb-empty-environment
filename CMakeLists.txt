cmake_minimum_required(VERSION 3.16)
project(w27_8ff461e968 CXX)
set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)

add_library(pacbti STATIC
    src/analyzer.cpp
)
target_include_directories(pacbti PUBLIC
    ${CMAKE_CURRENT_SOURCE_DIR}/include
)
target_compile_options(pacbti PRIVATE
    -Wall
    -Wextra
    -Wconversion
    -Wsign-conversion
    -Werror
)

enable_testing()

add_executable(pacbti_tests
    tests/test_main.cpp
    tests/test_label_lattice.cpp
    tests/test_xpaci_bypass.cpp
    tests/test_signing_oracle.cpp
    tests/test_modifier_conflict.cpp
    tests/test_bti_landing.cpp
    tests/test_exhaustive_cfg.cpp
    tests/test_key_domains.cpp
)
target_link_libraries(pacbti_tests PRIVATE pacbti)
target_compile_options(pacbti_tests PRIVATE
    -Wall
    -Wextra
    -Wconversion
    -Wsign-conversion
    -Werror
)
add_test(NAME pacbti_tests COMMAND pacbti_tests)
