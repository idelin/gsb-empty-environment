cmake_minimum_required(VERSION 3.16)

project(index_reclaim VERSION 1.0.0 LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

# On a macOS installation with only Command Line Tools selected, the
# xcrun-backed /usr/bin/ar launcher can fail even though the toolchain is
# usable.  Prefer the real archiver when it is available.
if(APPLE AND EXISTS "/Library/Developer/CommandLineTools/usr/bin/ar")
    if(CMAKE_AR STREQUAL "/usr/bin/ar")
        set(CMAKE_AR "/Library/Developer/CommandLineTools/usr/bin/ar"
            CACHE FILEPATH "Archiver" FORCE)
    endif()
    if(CMAKE_RANLIB STREQUAL "/usr/bin/ranlib")
        set(CMAKE_RANLIB
            "/Library/Developer/CommandLineTools/usr/bin/ranlib"
            CACHE FILEPATH "Ranlib" FORCE)
    endif()
endif()

find_package(Threads REQUIRED)

add_library(index_reclaim
    src/permission.cpp
    src/reference.cpp
)

target_include_directories(index_reclaim
    PUBLIC
        $<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}/include>
        $<INSTALL_INTERFACE:include>
)

target_link_libraries(index_reclaim PUBLIC Threads::Threads)

if(CMAKE_CXX_COMPILER_ID MATCHES "Clang|GNU")
    target_compile_options(index_reclaim PRIVATE -Wall -Wextra -Wpedantic)
endif()

include(CTest)

if(BUILD_TESTING)
    add_executable(index_reclaim_tests tests/test_permission.cpp)
    target_link_libraries(index_reclaim_tests PRIVATE index_reclaim)

    if(CMAKE_CXX_COMPILER_ID MATCHES "Clang|GNU")
        target_compile_options(index_reclaim_tests PRIVATE -Wall -Wextra -Wpedantic)
    endif()

    add_test(NAME index_reclaim_tests COMMAND index_reclaim_tests)
endif()
