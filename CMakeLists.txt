cmake_minimum_required(VERSION 3.16)
project(smmuv3_verify LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

add_library(smmuv3_verify INTERFACE)
target_include_directories(smmuv3_verify INTERFACE
    "$<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}/include>"
    "$<INSTALL_INTERFACE:include>")
target_compile_features(smmuv3_verify INTERFACE cxx_std_17)

option(SMMUV3_VERIFY_BUILD_TESTS "Build the SMMUv3 verification tests" ON)

if(SMMUV3_VERIFY_BUILD_TESTS)
    enable_testing()
    add_executable(smmuv3_verify_tests tests/smmuv3_verify_tests.cpp)
    target_link_libraries(smmuv3_verify_tests PRIVATE smmuv3_verify)
    if(CMAKE_CXX_COMPILER_ID MATCHES "Clang|GNU")
        target_compile_options(smmuv3_verify_tests PRIVATE
            -Wall -Wextra -Wconversion -Werror)
    endif()
    add_test(NAME smmuv3_verify_tests COMMAND smmuv3_verify_tests)
endif()
