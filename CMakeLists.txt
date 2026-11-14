cmake_minimum_required(VERSION 3.16)
project(netsec LANGUAGES CXX)
set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

# Command-line-tools-only macOS installs expose /usr/bin/ar as a wrapper that
# requires xcode-select. Use the real libtool from the active CLT when present.
if(APPLE AND EXISTS "/Library/Developer/CommandLineTools/usr/bin/libtool")
    set(CMAKE_CXX_ARCHIVE_CREATE
        "/Library/Developer/CommandLineTools/usr/bin/libtool -static -o <TARGET> <OBJECTS>")
    set(CMAKE_CXX_ARCHIVE_APPEND
        "/Library/Developer/CommandLineTools/usr/bin/libtool -static -o <TARGET> <OBJECTS>")
    set(CMAKE_CXX_ARCHIVE_FINISH "true")
endif()

add_library(netsec netsec.cpp)
target_include_directories(netsec PUBLIC "${CMAKE_CURRENT_SOURCE_DIR}")
enable_testing()
add_executable(netsec_tests tests/netsec_tests.cpp)
target_link_libraries(netsec_tests PRIVATE netsec)
add_test(NAME netsec_tests COMMAND netsec_tests)
