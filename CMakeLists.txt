cmake_minimum_required(VERSION 3.16)
project(netsec LANGUAGES CXX)

# Some macOS sandboxes expose /usr/bin/{ar,ranlib} as Xcode selection stubs
# even when the compiler itself is usable through its full toolchain path.
if(CMAKE_CXX_COMPILER)
    get_filename_component(_netsec_cxx_real "${CMAKE_CXX_COMPILER}" REALPATH)
    get_filename_component(_netsec_cxx_dir "${_netsec_cxx_real}" DIRECTORY)
    foreach(_netsec_tool AR RANLIB)
        string(TOLOWER "${_netsec_tool}" _netsec_tool_name)
        if(EXISTS "${_netsec_cxx_dir}/${_netsec_tool_name}" AND
           CMAKE_${_netsec_tool} STREQUAL "/usr/bin/${_netsec_tool_name}")
            set(
                CMAKE_${_netsec_tool}
                "${_netsec_cxx_dir}/${_netsec_tool_name}"
                CACHE FILEPATH "Tool from the selected C++ toolchain" FORCE)
        endif()
    endforeach()
    unset(_netsec_cxx_real)
    unset(_netsec_cxx_dir)
    unset(_netsec_tool)
    unset(_netsec_tool_name)
endif()

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)
add_library(netsec netsec.cpp)
target_include_directories(netsec PUBLIC "${CMAKE_CURRENT_SOURCE_DIR}")
add_library(netsec::netsec ALIAS netsec)
find_package(Threads REQUIRED)
target_link_libraries(netsec PUBLIC Threads::Threads)

include(CTest)
if(BUILD_TESTING)
    add_executable(netsec_tests tests/netsec_tests.cpp)
    target_link_libraries(netsec_tests PRIVATE netsec::netsec)
    add_test(NAME netsec_tests COMMAND netsec_tests)
endif()
