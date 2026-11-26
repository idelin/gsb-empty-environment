cmake_minimum_required(VERSION 3.16)
project(w27_2071d0b766 CXX)
set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)

# Embeddable DTLS 1.3 record-layer security library: zero external
# dependencies, strict warnings-as-errors for the whole project.
add_library(dtls13_record_layer STATIC
    src/types.cpp
    src/crypto.cpp
    src/record.cpp
    src/record_layer.cpp
    src/reassembly.cpp
    src/cookie.cpp
)
target_include_directories(dtls13_record_layer PUBLIC
    ${CMAKE_CURRENT_SOURCE_DIR}/include
)
target_compile_options(dtls13_record_layer PRIVATE
    -Wall
    -Wextra
    -Wconversion
    -Werror
    -Wpedantic
)

enable_testing()
add_executable(dtls13_tests
    tests/test_main.cpp
    tests/test_crypto.cpp
    tests/test_record.cpp
    tests/test_record_layer.cpp
    tests/test_reassembly.cpp
    tests/test_cookie.cpp
)
target_link_libraries(dtls13_tests PRIVATE dtls13_record_layer)
target_compile_options(dtls13_tests PRIVATE
    -Wall
    -Wextra
    -Wconversion
    -Werror
    -Wpedantic
)
add_test(NAME dtls13_tests COMMAND dtls13_tests)

# Optional ad-hoc fuzzing smoke target:
#   cmake -S . -B build -DDTLS13_BUILD_FUZZ=ON
option(DTLS13_BUILD_FUZZ "Build the ad-hoc fuzz smoke harness" OFF)
if(DTLS13_BUILD_FUZZ)
    add_executable(dtls13_fuzz_smoke tests/fuzz_smoke.cpp)
    target_link_libraries(dtls13_fuzz_smoke PRIVATE dtls13_record_layer)
    target_compile_options(dtls13_fuzz_smoke PRIVATE
        -Wall -Wextra -Wconversion -Werror -Wpedantic)
endif()
