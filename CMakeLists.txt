cmake_minimum_required(VERSION 3.20)
project(netsec VERSION 1.0.0 LANGUAGES CXX)

include(GNUInstallDirs)
include(CMakePackageConfigHelpers)

if(APPLE AND CMAKE_AR STREQUAL "/usr/bin/ar")
    find_program(NETSEC_CLT_AR
        NAMES ar
        PATHS /Library/Developer/CommandLineTools/usr/bin
        NO_DEFAULT_PATH
    )
    find_program(NETSEC_CLT_RANLIB
        NAMES ranlib
        PATHS /Library/Developer/CommandLineTools/usr/bin
        NO_DEFAULT_PATH
    )
    if(NETSEC_CLT_AR AND NETSEC_CLT_RANLIB)
        set(CMAKE_AR "${NETSEC_CLT_AR}" CACHE FILEPATH "Archiver" FORCE)
        set(CMAKE_RANLIB "${NETSEC_CLT_RANLIB}" CACHE FILEPATH "Ranlib" FORCE)
        set(CMAKE_CXX_ARCHIVE_CREATE "${NETSEC_CLT_AR} qc <TARGET> <LINK_FLAGS> <OBJECTS>")
        set(CMAKE_CXX_ARCHIVE_APPEND "${NETSEC_CLT_AR} q <TARGET> <LINK_FLAGS> <OBJECTS>")
        set(CMAKE_CXX_ARCHIVE_FINISH "${NETSEC_CLT_RANLIB} <TARGET>")
    endif()
endif()

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

add_library(netsec netsec.cpp)
add_library(netsec::netsec ALIAS netsec)
target_compile_features(netsec PUBLIC cxx_std_20)
target_include_directories(netsec
    PUBLIC
        $<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}>
        $<INSTALL_INTERFACE:${CMAKE_INSTALL_INCLUDEDIR}>
)

if(MSVC)
    target_compile_options(netsec PRIVATE /W4 /permissive-)
else()
    target_compile_options(netsec PRIVATE -Wall -Wextra -Wpedantic)
endif()

enable_testing()

add_executable(netsec_tests tests/netsec_tests.cpp)
target_link_libraries(netsec_tests PRIVATE netsec::netsec)
if(MSVC)
    target_compile_options(netsec_tests PRIVATE /W4 /permissive-)
else()
    target_compile_options(netsec_tests PRIVATE -Wall -Wextra -Wpedantic)
endif()
add_test(NAME netsec_tests COMMAND netsec_tests)

install(TARGETS netsec
    EXPORT netsecTargets
    ARCHIVE DESTINATION ${CMAKE_INSTALL_LIBDIR}
    LIBRARY DESTINATION ${CMAKE_INSTALL_LIBDIR}
    RUNTIME DESTINATION ${CMAKE_INSTALL_BINDIR}
)
install(FILES netsec.hpp DESTINATION ${CMAKE_INSTALL_INCLUDEDIR})
install(EXPORT netsecTargets
    FILE netsecTargets.cmake
    NAMESPACE netsec::
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
