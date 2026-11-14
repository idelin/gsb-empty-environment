cmake_minimum_required(VERSION 3.16)
project(netsec VERSION 1.0.0 LANGUAGES CXX)

if(CMAKE_SOURCE_DIR STREQUAL CMAKE_CURRENT_SOURCE_DIR)
    set(NETSEC_TESTS_DEFAULT ON)
else()
    set(NETSEC_TESTS_DEFAULT OFF)
endif()
option(NETSEC_BUILD_TESTS "Build the netsec test suite" ${NETSEC_TESTS_DEFAULT})

add_library(netsec netsec.cpp)
add_library(netsec::netsec ALIAS netsec)
target_compile_features(netsec PUBLIC cxx_std_20)
set_target_properties(netsec PROPERTIES
    CXX_EXTENSIONS OFF
    CXX_STANDARD 20
    CXX_STANDARD_REQUIRED ON
)
target_include_directories(netsec PUBLIC
    $<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}>
    $<INSTALL_INTERFACE:include>
)

if(NETSEC_BUILD_TESTS)
    enable_testing()
    add_executable(netsec_tests tests/netsec_tests.cpp)
    target_link_libraries(netsec_tests PRIVATE netsec::netsec)
    add_test(NAME netsec_tests COMMAND netsec_tests)

    # Internal crypto test: includes the implementation so the otherwise
    # private SHA-256/HMAC helpers can be checked against published vectors.
    add_executable(netsec_crypto_tests tests/crypto_tests.cpp)
    target_compile_features(netsec_crypto_tests PRIVATE cxx_std_20)
    set_target_properties(netsec_crypto_tests PROPERTIES
        CXX_EXTENSIONS OFF
        CXX_STANDARD 20
        CXX_STANDARD_REQUIRED ON
    )
    target_include_directories(netsec_crypto_tests PRIVATE
        "${CMAKE_CURRENT_SOURCE_DIR}"
    )
    add_test(NAME netsec_crypto_tests COMMAND netsec_crypto_tests)
endif()
