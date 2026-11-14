cmake_minimum_required(VERSION 3.16)
project(netsec VERSION 1.0.0 LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

include(GNUInstallDirs)

add_library(netsec netsec.cpp)
add_library(netsec::netsec ALIAS netsec)
target_include_directories(
    netsec
    PUBLIC
        $<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}>
        $<INSTALL_INTERFACE:${CMAKE_INSTALL_INCLUDEDIR}>
)
target_compile_features(netsec PUBLIC cxx_std_20)
set_target_properties(netsec PROPERTIES POSITION_INDEPENDENT_CODE ON)

include(CMakePackageConfigHelpers)

install(
    TARGETS netsec
    EXPORT netsecTargets
    ARCHIVE DESTINATION ${CMAKE_INSTALL_LIBDIR}
    LIBRARY DESTINATION ${CMAKE_INSTALL_LIBDIR}
    RUNTIME DESTINATION ${CMAKE_INSTALL_BINDIR}
)
install(FILES netsec.hpp DESTINATION ${CMAKE_INSTALL_INCLUDEDIR})
install(
    EXPORT netsecTargets
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
install(
    FILES
        ${CMAKE_CURRENT_BINARY_DIR}/netsecConfig.cmake
        ${CMAKE_CURRENT_BINARY_DIR}/netsecConfigVersion.cmake
    DESTINATION ${CMAKE_INSTALL_LIBDIR}/cmake/netsec
)

if(CMAKE_CURRENT_SOURCE_DIR STREQUAL CMAKE_SOURCE_DIR)
    include(CTest)
    if(BUILD_TESTING)
        add_subdirectory(tests)
    endif()
endif()
