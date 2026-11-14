cmake_minimum_required(VERSION 3.16)
project(netsec LANGUAGES CXX)

# Some sandboxed macOS images expose /usr/bin/ar as an xcode-select shim.
# Prefer the CommandLineTools binary when it is present.
if(APPLE AND EXISTS "/Library/Developer/CommandLineTools/usr/bin/ar")
    set(CMAKE_AR "/Library/Developer/CommandLineTools/usr/bin/ar")
    if(EXISTS "/Library/Developer/CommandLineTools/usr/bin/ranlib")
        set(CMAKE_RANLIB "/Library/Developer/CommandLineTools/usr/bin/ranlib")
    endif()
endif()

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)
add_library(netsec netsec.cpp)
target_include_directories(netsec PUBLIC "${CMAKE_CURRENT_SOURCE_DIR}")
target_compile_features(netsec PUBLIC cxx_std_20)
enable_testing()

add_executable(netsec_tests tests/test_netsec.cpp)
target_link_libraries(netsec_tests PRIVATE netsec)
target_compile_definitions(
    netsec_tests
    PRIVATE NETSEC_FIXTURE_DIR="${CMAKE_CURRENT_SOURCE_DIR}/tests/fixtures")
add_test(NAME netsec_tests COMMAND netsec_tests)
