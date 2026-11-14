cmake_minimum_required(VERSION 3.16)
project(netsec LANGUAGES CXX)

# Toolchain wrapper directories on macOS may not contain ar/ranlib, causing
# CMake to fall back to /usr/bin tools that require xcode-select. If the real
# compiler has sibling binutils, use those instead.
if(APPLE)
    get_filename_component(_netsec_cxx_real "${CMAKE_CXX_COMPILER}" REALPATH)
    get_filename_component(_netsec_cxx_dir "${_netsec_cxx_real}" DIRECTORY)
    foreach(_netsec_tool IN ITEMS ar ranlib)
        if(EXISTS "${_netsec_cxx_dir}/${_netsec_tool}")
            string(TOUPPER "${_netsec_tool}" _netsec_tool_upper)
            set(CMAKE_${_netsec_tool_upper}
                "${_netsec_cxx_dir}/${_netsec_tool}"
                CACHE FILEPATH "Archiver tool" FORCE)
        endif()
    endforeach()
endif()

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)
add_library(netsec netsec.cpp)
add_library(netsec::netsec ALIAS netsec)
target_include_directories(netsec PUBLIC "${CMAKE_CURRENT_SOURCE_DIR}")
target_compile_features(netsec PUBLIC cxx_std_20)
enable_testing()
add_executable(netsec_tests tests/netsec_tests.cpp)
target_link_libraries(netsec_tests PRIVATE netsec)
add_test(NAME netsec_tests COMMAND netsec_tests)
