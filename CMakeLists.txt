cmake_minimum_required(VERSION 3.16)
project(netsec VERSION 1.0.0 LANGUAGES CXX)
set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

include(GNUInstallDirs)

# On some managed macOS images /usr/bin/ar and /usr/bin/ranlib are
# xcode-select shims.  Prefer the real tools that ship next to clang.
if(APPLE)
    get_filename_component(_netsec_cxx_real "${CMAKE_CXX_COMPILER}" REALPATH)
    get_filename_component(_netsec_cxx_dir "${_netsec_cxx_real}" DIRECTORY)
    if(EXISTS "${_netsec_cxx_dir}/ar" AND EXISTS "${_netsec_cxx_dir}/ranlib")
        set(CMAKE_AR "${_netsec_cxx_dir}/ar" CACHE FILEPATH "" FORCE)
        set(CMAKE_RANLIB "${_netsec_cxx_dir}/ranlib" CACHE FILEPATH "" FORCE)
        set(CMAKE_AR "${_netsec_cxx_dir}/ar")
        set(CMAKE_RANLIB "${_netsec_cxx_dir}/ranlib")
    endif()
endif()

add_library(netsec netsec.cpp netsec_crypto.cpp)
add_library(netsec::netsec ALIAS netsec)
target_include_directories(netsec
    PUBLIC
        $<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}>
        $<INSTALL_INTERFACE:${CMAKE_INSTALL_INCLUDEDIR}>
)
target_compile_features(netsec PUBLIC cxx_std_20)

include(CMakePackageConfigHelpers)

install(TARGETS netsec
    EXPORT netsecTargets
    ARCHIVE DESTINATION ${CMAKE_INSTALL_LIBDIR}
    LIBRARY DESTINATION ${CMAKE_INSTALL_LIBDIR}
    RUNTIME DESTINATION ${CMAKE_INSTALL_BINDIR}
)
install(FILES netsec.hpp DESTINATION ${CMAKE_INSTALL_INCLUDEDIR})

install(EXPORT netsecTargets
    NAMESPACE netsec::
    FILE netsecTargets.cmake
    DESTINATION ${CMAKE_INSTALL_LIBDIR}/cmake/netsec
)

configure_package_config_file(
    cmake/netsecConfig.cmake.in
    ${CMAKE_CURRENT_BINARY_DIR}/netsecConfig.cmake
    INSTALL_DESTINATION ${CMAKE_INSTALL_LIBDIR}/cmake/netsec
)
write_basic_package_version_file(
    ${CMAKE_CURRENT_BINARY_DIR}/netsecConfigVersion.cmake
    VERSION ${PROJECT_VERSION}
    COMPATIBILITY SameMajorVersion
)
install(FILES
    ${CMAKE_CURRENT_BINARY_DIR}/netsecConfig.cmake
    ${CMAKE_CURRENT_BINARY_DIR}/netsecConfigVersion.cmake
    DESTINATION ${CMAKE_INSTALL_LIBDIR}/cmake/netsec
)

include(CTest)

if(BUILD_TESTING)
    add_executable(netsec_snapshot_tests tests/snapshot_ownership_test.cpp)
    target_link_libraries(netsec_snapshot_tests PRIVATE netsec)
    target_include_directories(netsec_snapshot_tests PRIVATE "${CMAKE_CURRENT_SOURCE_DIR}")
    add_test(NAME snapshot_ownership COMMAND netsec_snapshot_tests)
endif()
