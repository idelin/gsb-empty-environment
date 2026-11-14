cmake_minimum_required(VERSION 3.16)
project(netsec VERSION 1.0.0 LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

add_library(netsec netsec.cpp)
add_library(netsec::netsec ALIAS netsec)
set_target_properties(netsec PROPERTIES POSITION_INDEPENDENT_CODE ON)
target_compile_features(netsec PUBLIC cxx_std_20)
target_include_directories(
  netsec
  PUBLIC
    $<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}>
    $<INSTALL_INTERFACE:include>
)

include(CTest)
if(BUILD_TESTING)
  add_executable(netsec_tests tests/stream_identity_test.cpp)
  target_link_libraries(netsec_tests PRIVATE netsec::netsec)
  add_test(NAME netsec_stream_identity COMMAND netsec_tests)
endif()

include(GNUInstallDirs)
include(CMakePackageConfigHelpers)

install(TARGETS netsec EXPORT netsecTargets
        ARCHIVE DESTINATION ${CMAKE_INSTALL_LIBDIR}
        LIBRARY DESTINATION ${CMAKE_INSTALL_LIBDIR}
        RUNTIME DESTINATION ${CMAKE_INSTALL_BINDIR})
install(FILES netsec.hpp DESTINATION ${CMAKE_INSTALL_INCLUDEDIR})

configure_package_config_file(
  cmake/netsecConfig.cmake.in
  "${CMAKE_CURRENT_BINARY_DIR}/netsecConfig.cmake"
  INSTALL_DESTINATION ${CMAKE_INSTALL_LIBDIR}/cmake/netsec
)
write_basic_package_version_file(
  "${CMAKE_CURRENT_BINARY_DIR}/netsecConfigVersion.cmake"
  VERSION ${PROJECT_VERSION}
  COMPATIBILITY SameMajorVersion
)
install(FILES
        "${CMAKE_CURRENT_BINARY_DIR}/netsecConfig.cmake"
        "${CMAKE_CURRENT_BINARY_DIR}/netsecConfigVersion.cmake"
        DESTINATION ${CMAKE_INSTALL_LIBDIR}/cmake/netsec)
install(EXPORT netsecTargets
        FILE netsecTargets.cmake
        NAMESPACE netsec::
        DESTINATION ${CMAKE_INSTALL_LIBDIR}/cmake/netsec)
