cmake_minimum_required(VERSION 3.16)
project(netsec VERSION 1.0.0 LANGUAGES CXX)
set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)
find_package(Threads REQUIRED)
include(GNUInstallDirs)

add_library(netsec pidfd_capability.cpp)
target_include_directories(netsec PUBLIC
        "$<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}>"
        "$<INSTALL_INTERFACE:${CMAKE_INSTALL_INCLUDEDIR}>")
target_link_libraries(netsec PUBLIC Threads::Threads)

include(CTest)
if(BUILD_TESTING)
  add_executable(pidfd_capability_test pidfd_capability_test.cpp)
  target_link_libraries(pidfd_capability_test PRIVATE netsec)
  add_test(NAME pidfd_capability_test COMMAND pidfd_capability_test)
endif()

install(TARGETS netsec
        EXPORT netsecTargets
        ARCHIVE DESTINATION "${CMAKE_INSTALL_LIBDIR}")
install(FILES netsec.hpp pidfd_capability.hpp
        DESTINATION "${CMAKE_INSTALL_INCLUDEDIR}")
install(EXPORT netsecTargets
        FILE netsecTargets.cmake
        NAMESPACE netsec::
        DESTINATION "${CMAKE_INSTALL_LIBDIR}/cmake/netsec")

include(CMakePackageConfigHelpers)
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
