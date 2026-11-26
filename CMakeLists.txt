cmake_minimum_required(VERSION 3.20)

project(dwarf_cfi
    VERSION 1.0.0
    DESCRIPTION "DWARF v5 CFI decoder and frame-table evaluator"
    LANGUAGES CXX)

include(GNUInstallDirs)
include(CTest)

add_library(dwarf_cfi STATIC
    src/cfi.cpp)
add_library(dwarf::cfi ALIAS dwarf_cfi)

target_compile_features(dwarf_cfi PUBLIC cxx_std_20)
target_include_directories(dwarf_cfi
    PUBLIC
        $<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}/include>
        $<INSTALL_INTERFACE:${CMAKE_INSTALL_INCLUDEDIR}>)

if(CMAKE_CXX_COMPILER_ID MATCHES "GNU|Clang")
    target_compile_options(dwarf_cfi PRIVATE -Wall -Wextra -Wconversion -Wshadow)
endif()

add_executable(dwarf_cfi_dump tools/cfi_dump.cpp)
target_link_libraries(dwarf_cfi_dump PRIVATE dwarf::cfi)

install(TARGETS dwarf_cfi
    EXPORT dwarf_cfiTargets
    ARCHIVE DESTINATION ${CMAKE_INSTALL_LIBDIR})
install(TARGETS dwarf_cfi_dump
    RUNTIME DESTINATION ${CMAKE_INSTALL_BINDIR})
install(DIRECTORY include/ DESTINATION ${CMAKE_INSTALL_INCLUDEDIR})
install(EXPORT dwarf_cfiTargets
    FILE dwarf_cfiTargets.cmake
    NAMESPACE dwarf::
    DESTINATION ${CMAKE_INSTALL_LIBDIR}/cmake/dwarf_cfi)

if(BUILD_TESTING)
    add_executable(dwarf_cfi_tests tests/test_cfi.cpp)
    target_link_libraries(dwarf_cfi_tests PRIVATE dwarf::cfi)
    if(CMAKE_CXX_COMPILER_ID MATCHES "GNU|Clang")
        target_compile_options(dwarf_cfi_tests PRIVATE -Wall -Wextra)
    endif()
    add_test(NAME dwarf_cfi_tests COMMAND dwarf_cfi_tests)
endif()
