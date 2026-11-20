cmake_minimum_required(VERSION 3.20)

project(wide_table_query_auth VERSION 1.0.0 LANGUAGES CXX)

add_library(wide_table_query_auth INTERFACE)
add_library(wide_table::query_auth ALIAS wide_table_query_auth)
set_target_properties(wide_table_query_auth PROPERTIES EXPORT_NAME query_auth)

target_compile_features(wide_table_query_auth INTERFACE cxx_std_20)
target_include_directories(
    wide_table_query_auth
    INTERFACE
        $<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}/include>
        $<INSTALL_INTERFACE:include>)

include(GNUInstallDirs)
include(CTest)
include(CMakePackageConfigHelpers)

if(BUILD_TESTING)
    add_executable(query_auth_tests tests/query_auth_test.cpp)
    target_link_libraries(query_auth_tests PRIVATE wide_table::query_auth)
    if(MSVC)
        target_compile_options(query_auth_tests PRIVATE /W4)
    else()
        target_compile_options(
            query_auth_tests PRIVATE -Wall -Wextra -Wpedantic)
    endif()
    add_test(NAME query_auth_tests COMMAND query_auth_tests)
endif()

install(TARGETS wide_table_query_auth
        EXPORT wide_table_query_auth_targets)
install(DIRECTORY include/ DESTINATION ${CMAKE_INSTALL_INCLUDEDIR})

set(package_dir "${CMAKE_INSTALL_LIBDIR}/cmake/wide_table_query_auth")
configure_package_config_file(
    cmake/wide_table_query_authConfig.cmake.in
    "${CMAKE_CURRENT_BINARY_DIR}/wide_table_query_authConfig.cmake"
    INSTALL_DESTINATION "${package_dir}")
write_basic_package_version_file(
    "${CMAKE_CURRENT_BINARY_DIR}/wide_table_query_authConfigVersion.cmake"
    VERSION ${PROJECT_VERSION}
    COMPATIBILITY SameMajorVersion)

install(EXPORT wide_table_query_auth_targets
        FILE wide_table_query_authTargets.cmake
        NAMESPACE wide_table::
        DESTINATION "${package_dir}")
install(
    FILES
        "${CMAKE_CURRENT_BINARY_DIR}/wide_table_query_authConfig.cmake"
        "${CMAKE_CURRENT_BINARY_DIR}/wide_table_query_authConfigVersion.cmake"
    DESTINATION "${package_dir}")
