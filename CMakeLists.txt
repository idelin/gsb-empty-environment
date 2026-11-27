cmake_minimum_required(VERSION 3.16)
project(w28_892fee4882 LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

# Some headless macOS installations leave /usr/bin/ar as an xcrun shim that
# fails when no developer directory is selected.  Use the actual tool when the
# cache still contains the default shim paths.
if(APPLE AND CMAKE_AR STREQUAL "/usr/bin/ar" AND
   EXISTS "/Library/Developer/CommandLineTools/usr/bin/ar")
  set(CMAKE_AR "/Library/Developer/CommandLineTools/usr/bin/ar"
      CACHE FILEPATH "Archiver" FORCE)
endif()
if(APPLE AND CMAKE_RANLIB STREQUAL "/usr/bin/ranlib" AND
   EXISTS "/Library/Developer/CommandLineTools/usr/bin/ranlib")
  set(CMAKE_RANLIB "/Library/Developer/CommandLineTools/usr/bin/ranlib"
      CACHE FILEPATH "Archive indexer" FORCE)
endif()

add_library(joint_admission STATIC src/joint_admission.cpp)
target_include_directories(
  joint_admission
  PUBLIC
    $<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}/include>
    $<INSTALL_INTERFACE:include>)
target_compile_features(joint_admission PUBLIC cxx_std_17)

include(CTest)
if(BUILD_TESTING)
  add_executable(joint_admission_tests tests/joint_admission_test.cpp)
  target_link_libraries(joint_admission_tests PRIVATE joint_admission)
  add_test(NAME joint_admission_tests COMMAND joint_admission_tests)
endif()

install(TARGETS joint_admission ARCHIVE DESTINATION lib)
install(DIRECTORY include/ DESTINATION include)
