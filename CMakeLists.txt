cmake_minimum_required(VERSION 3.16)
project(iommu_guard LANGUAGES CXX)

if(APPLE)
    find_program(IOMMU_GUARD_AR
        NAMES ar
        PATHS /Library/Developer/CommandLineTools/usr/bin
        NO_DEFAULT_PATH
    )
    find_program(IOMMU_GUARD_RANLIB
        NAMES ranlib
        PATHS /Library/Developer/CommandLineTools/usr/bin
        NO_DEFAULT_PATH
    )
    if(IOMMU_GUARD_AR)
        set(CMAKE_AR "${IOMMU_GUARD_AR}" CACHE FILEPATH "Static archiver" FORCE)
    endif()
    if(IOMMU_GUARD_RANLIB)
        set(CMAKE_RANLIB "${IOMMU_GUARD_RANLIB}" CACHE FILEPATH "Static archive indexer" FORCE)
    endif()
endif()

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

add_library(iommu_guard STATIC
    src/audit.cpp
    src/core.cpp
    src/dmar.cpp
    src/pci.cpp
    src/vtd.cpp
)
target_include_directories(iommu_guard
    PUBLIC
        ${CMAKE_CURRENT_SOURCE_DIR}/include
    PRIVATE
        ${CMAKE_CURRENT_SOURCE_DIR}/src
)
target_compile_features(iommu_guard PUBLIC cxx_std_17)
target_compile_options(iommu_guard PRIVATE
    -Wall -Wextra -Wconversion -Werror
)

include(CTest)
if(BUILD_TESTING)
    add_executable(iommu_guard_tests tests/iommu_guard_tests.cpp)
    target_link_libraries(iommu_guard_tests PRIVATE iommu_guard)
    target_compile_options(iommu_guard_tests PRIVATE
        -Wall -Wextra -Wconversion -Werror
    )
    add_test(NAME iommu_guard_tests COMMAND iommu_guard_tests)
endif()
