cmake_minimum_required(VERSION 3.16)
project(netsec VERSION 1.0.0 LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

if(APPLE
   AND EXISTS "/Library/Developer/CommandLineTools/usr/bin/ar"
   AND EXISTS "/Library/Developer/CommandLineTools/usr/bin/ranlib")
  set(CMAKE_AR "/Library/Developer/CommandLineTools/usr/bin/ar"
      CACHE FILEPATH "Archiver" FORCE)
  set(CMAKE_RANLIB "/Library/Developer/CommandLineTools/usr/bin/ranlib"
      CACHE FILEPATH "Archive indexer" FORCE)
endif()

include(GNUInstallDirs)

add_library(netsec netsec.cpp)
target_compile_features(netsec PUBLIC cxx_std_20)
target_include_directories(
  netsec PUBLIC
  "$<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}>"
  "$<INSTALL_INTERFACE:${CMAKE_INSTALL_INCLUDEDIR}>")

if(MSVC)
  target_compile_options(netsec PRIVATE /W4)
else()
  target_compile_options(netsec PRIVATE -Wall -Wextra -Wpedantic)
endif()

include(CTest)
if(BUILD_TESTING)
  add_executable(netsec_tests tests/chip_identity_tests.cpp)
  target_link_libraries(netsec_tests PRIVATE netsec)
  add_test(NAME chip_identity_authentication COMMAND netsec_tests)
endif()

install(TARGETS netsec EXPORT netsecTargets
        ARCHIVE DESTINATION "${CMAKE_INSTALL_LIBDIR}"
        LIBRARY DESTINATION "${CMAKE_INSTALL_LIBDIR}"
        RUNTIME DESTINATION "${CMAKE_INSTALL_BINDIR}")
install(FILES netsec.hpp DESTINATION "${CMAKE_INSTALL_INCLUDEDIR}")
install(EXPORT netsecTargets NAMESPACE netsec::
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
