cmake_minimum_required(VERSION 3.16)
project(netsec VERSION 1.0.0 LANGUAGES CXX)
set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

if(APPLE AND (NOT CMAKE_AR OR CMAKE_AR STREQUAL "/usr/bin/ar"))
    get_filename_component(_netsec_cxx_real "${CMAKE_CXX_COMPILER}" REALPATH)
    get_filename_component(_netsec_toolchain_bin "${_netsec_cxx_real}" DIRECTORY)
    if(EXISTS "${_netsec_toolchain_bin}/ar" AND
       EXISTS "${_netsec_toolchain_bin}/ranlib")
        set(CMAKE_AR "${_netsec_toolchain_bin}/ar"
            CACHE FILEPATH "Archiver aligned with the C++ compiler" FORCE)
        set(CMAKE_RANLIB "${_netsec_toolchain_bin}/ranlib"
            CACHE FILEPATH "Ranlib aligned with the C++ compiler" FORCE)
    endif()
endif()

include(GNUInstallDirs)
find_package(Threads REQUIRED)
add_library(netsec netsec.cpp)
add_library(netsec::netsec ALIAS netsec)
target_include_directories(netsec PUBLIC
    "$<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}>"
    "$<INSTALL_INTERFACE:${CMAKE_INSTALL_INCLUDEDIR}>")
target_compile_features(netsec PUBLIC cxx_std_20)
target_link_libraries(netsec PUBLIC Threads::Threads)
include(CTest)

if(BUILD_TESTING)
    add_executable(netsec_tests tests/netsec_tests.cpp)
    target_link_libraries(netsec_tests PRIVATE netsec::netsec)
    add_test(NAME netsec_tests COMMAND netsec_tests)
endif()

include(CMakePackageConfigHelpers)

install(TARGETS netsec
        EXPORT netsecTargets
        ARCHIVE DESTINATION "${CMAKE_INSTALL_LIBDIR}"
        LIBRARY DESTINATION "${CMAKE_INSTALL_LIBDIR}"
        RUNTIME DESTINATION "${CMAKE_INSTALL_BINDIR}"
        INCLUDES DESTINATION "${CMAKE_INSTALL_INCLUDEDIR}")
install(FILES netsec.hpp DESTINATION "${CMAKE_INSTALL_INCLUDEDIR}")
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
