cmake_minimum_required(VERSION 3.20)
project(netsec VERSION 1.0.0 LANGUAGES CXX)

option(NETSEC_BUILD_TESTS "Build the side-channel verification tests" ON)

include(GNUInstallDirs)

add_library(netsec netsec.cpp)
add_library(netsec::netsec ALIAS netsec)
target_compile_features(netsec PUBLIC cxx_std_20)
set_target_properties(netsec PROPERTIES
    CXX_EXTENSIONS OFF
    PUBLIC_HEADER netsec.hpp
    VERSION "${PROJECT_VERSION}"
    SOVERSION "${PROJECT_VERSION_MAJOR}")
target_include_directories(netsec PUBLIC
    "$<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}>"
    "$<INSTALL_INTERFACE:${CMAKE_INSTALL_INCLUDEDIR}>")

if(CMAKE_CXX_COMPILER_ID MATCHES "Clang|GNU")
    target_compile_options(netsec PRIVATE -Wall -Wextra -Wpedantic)
endif()

include(CMakePackageConfigHelpers)

if(WIN32)
    target_link_libraries(netsec PRIVATE bcrypt)
endif()

install(TARGETS netsec
    EXPORT netsecTargets
    ARCHIVE DESTINATION "${CMAKE_INSTALL_LIBDIR}"
    LIBRARY DESTINATION "${CMAKE_INSTALL_LIBDIR}"
    RUNTIME DESTINATION "${CMAKE_INSTALL_BINDIR}"
    PUBLIC_HEADER DESTINATION "${CMAKE_INSTALL_INCLUDEDIR}")
install(EXPORT netsecTargets
    FILE netsecTargets.cmake
    NAMESPACE netsec::
    DESTINATION "${CMAKE_INSTALL_LIBDIR}/cmake/netsec")

configure_package_config_file(
    cmake/netsecConfig.cmake.in
    "${CMAKE_CURRENT_BINARY_DIR}/netsecConfig.cmake"
    INSTALL_DESTINATION "${CMAKE_INSTALL_LIBDIR}/cmake/netsec")
write_basic_package_version_file(
    "${CMAKE_CURRENT_BINARY_DIR}/netsecConfigVersion.cmake"
    VERSION "${PROJECT_VERSION}"
    COMPATIBILITY SameMajorVersion)
install(FILES
    "${CMAKE_CURRENT_BINARY_DIR}/netsecConfig.cmake"
    "${CMAKE_CURRENT_BINARY_DIR}/netsecConfigVersion.cmake"
    DESTINATION "${CMAKE_INSTALL_LIBDIR}/cmake/netsec")

if(NETSEC_BUILD_TESTS)
    enable_testing()
    add_executable(netsec_tests tests/netsec_tests.cpp)
    target_link_libraries(netsec_tests PRIVATE netsec::netsec)
    if(CMAKE_CXX_COMPILER_ID MATCHES "Clang|GNU")
        target_compile_options(netsec_tests PRIVATE -Wall -Wextra -Wpedantic)
    endif()
    add_test(NAME netsec_tests COMMAND netsec_tests)
endif()
