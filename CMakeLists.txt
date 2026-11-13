cmake_minimum_required(VERSION 3.16)
project(netsec LANGUAGES CXX)

if(APPLE AND EXISTS "/Library/Developer/CommandLineTools/usr/bin/ar")
    if(CMAKE_AR STREQUAL "/usr/bin/ar")
        set(CMAKE_AR "/Library/Developer/CommandLineTools/usr/bin/ar"
            CACHE FILEPATH "" FORCE)
        set(CMAKE_RANLIB "/Library/Developer/CommandLineTools/usr/bin/ranlib"
            CACHE FILEPATH "" FORCE)
    endif()
    if(CMAKE_LINKER STREQUAL "/usr/bin/ld")
        set(CMAKE_LINKER "/Library/Developer/CommandLineTools/usr/bin/ld"
            CACHE FILEPATH "" FORCE)
    endif()
endif()

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)
add_library(netsec netsec.cpp)
target_include_directories(netsec PUBLIC "${CMAKE_CURRENT_SOURCE_DIR}")
enable_testing()

add_executable(netsec_tests tests/test_netsec.cpp)
target_link_libraries(netsec_tests PRIVATE netsec)
add_test(NAME netsec_tests COMMAND netsec_tests)
