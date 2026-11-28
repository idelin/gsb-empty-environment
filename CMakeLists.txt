cmake_minimum_required(VERSION 3.16)
project(pe_delay_reloc_audit LANGUAGES CXX)

# On headless macOS installs /usr/bin/ar and /usr/bin/ld can be xcrun shims
# that fail without a selected developer directory. Prefer the tools installed
# next to the actual compiler when that layout is present.
if(APPLE)
    get_filename_component(_pe_audit_cxx_real
        "${CMAKE_CXX_COMPILER}" REALPATH)
    get_filename_component(_pe_audit_tool_dir
        "${_pe_audit_cxx_real}" DIRECTORY)
    if(EXISTS "${_pe_audit_tool_dir}/ar")
        if(NOT CMAKE_AR OR CMAKE_AR STREQUAL "/usr/bin/ar")
            set(CMAKE_AR "${_pe_audit_tool_dir}/ar"
                CACHE FILEPATH "Archiver" FORCE)
        endif()
        if(EXISTS "${_pe_audit_tool_dir}/ranlib" AND
           (NOT CMAKE_RANLIB OR CMAKE_RANLIB STREQUAL "/usr/bin/ranlib"))
            set(CMAKE_RANLIB "${_pe_audit_tool_dir}/ranlib"
                CACHE FILEPATH "Archive ranlib" FORCE)
        endif()
    endif()
    if(EXISTS "${_pe_audit_tool_dir}/ld")
        if(NOT CMAKE_LINKER OR CMAKE_LINKER STREQUAL "/usr/bin/ld")
            set(CMAKE_LINKER "${_pe_audit_tool_dir}/ld"
                CACHE FILEPATH "Linker" FORCE)
        endif()
    endif()
endif()

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

function(pe_audit_warnings target)
    if(MSVC)
        target_compile_options(${target} PRIVATE /W4 /WX)
    else()
        target_compile_options(${target} PRIVATE
            -Wall
            -Wextra
            -Wconversion
            -Werror)
    endif()
endfunction()

add_library(pe_audit STATIC
    src/pe_audit.cpp)
add_library(pe_audit::pe_audit ALIAS pe_audit)
target_include_directories(pe_audit PUBLIC
    ${CMAKE_CURRENT_SOURCE_DIR}/include)
target_compile_features(pe_audit PUBLIC cxx_std_17)
pe_audit_warnings(pe_audit)

add_executable(pe_audit_cli
    tools/pe_audit_cli.cpp)
target_link_libraries(pe_audit_cli PRIVATE pe_audit)
pe_audit_warnings(pe_audit_cli)

include(CTest)
if(BUILD_TESTING)
    add_executable(pe_audit_tests
        tests/pe_audit_tests.cpp)
    target_link_libraries(pe_audit_tests PRIVATE pe_audit)
    pe_audit_warnings(pe_audit_tests)
    add_test(NAME pe_audit_tests COMMAND pe_audit_tests)
endif()
