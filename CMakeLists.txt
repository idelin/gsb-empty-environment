cmake_minimum_required(VERSION 3.20)
project(netsec LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

# Apple Clang only accepts C++20 named-module syntax with -fcxx-modules.
if(CMAKE_CXX_COMPILER_ID MATCHES "Clang")
  add_compile_options(-fcxx-modules)
endif()

set(MODULE_PCM "${CMAKE_BINARY_DIR}/netsec.onboarding.pcm")

# Precompile the importable module interface unit into a binary module.
add_custom_command(
  OUTPUT "${MODULE_PCM}"
  COMMAND ${CMAKE_CXX_COMPILER}
          -std=c++20 -fcxx-modules -x c++-module --precompile
          "${CMAKE_CURRENT_SOURCE_DIR}/src/onboarding.cppm"
          -o "${MODULE_PCM}"
  DEPENDS "${CMAKE_CURRENT_SOURCE_DIR}/src/onboarding.cppm"
  VERBATIM)

add_library(netsec_onboarding OBJECT
  "${CMAKE_CURRENT_SOURCE_DIR}/src/onboarding.impl.cpp"
  "${MODULE_PCM}")
target_compile_options(netsec_onboarding PRIVATE
  "-fmodule-file=netsec.onboarding=${MODULE_PCM}")
target_include_directories(netsec_onboarding PUBLIC
  "${CMAKE_CURRENT_SOURCE_DIR}")

enable_testing()
add_executable(onboarding_test tests/onboarding_test.cpp)
target_compile_options(onboarding_test PRIVATE
  "-fmodule-file=netsec.onboarding=${MODULE_PCM}")
target_sources(onboarding_test PRIVATE $<TARGET_OBJECTS:netsec_onboarding>)
add_test(NAME onboarding COMMAND onboarding_test)
