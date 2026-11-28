cmake_minimum_required(VERSION 3.16)
project(pkvm_audit LANGUAGES CXX)

set(CMAKE_CXX_EXTENSIONS OFF)

add_library(pkvm_audit INTERFACE)
add_library(pkvm::audit ALIAS pkvm_audit)

target_compile_features(pkvm_audit INTERFACE cxx_std_17)
target_include_directories(
    pkvm_audit
    INTERFACE
        $<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}/include>
        $<INSTALL_INTERFACE:include>)

option(PKVM_AUDIT_STRICT_WARNINGS "Build with strict warning checks" ON)
if(PKVM_AUDIT_STRICT_WARNINGS)
    if(MSVC)
        target_compile_options(pkvm_audit INTERFACE /W4 /WX /permissive-)
    else()
        target_compile_options(
            pkvm_audit
            INTERFACE
                -Wall
                -Wextra
                -Wconversion
                -Werror)
    endif()
endif()

include(CTest)
if(BUILD_TESTING)
    add_executable(pkvm_audit_tests tests/test_pkvm.cpp)
    target_link_libraries(pkvm_audit_tests PRIVATE pkvm::audit)
    add_test(NAME pkvm_audit_tests COMMAND pkvm_audit_tests)
endif()
