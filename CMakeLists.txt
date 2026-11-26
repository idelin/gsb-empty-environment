cmake_minimum_required(VERSION 3.16)

if(APPLE)
    set(_iommu_clt_bin "/Library/Developer/CommandLineTools/usr/bin")
    if(EXISTS "${_iommu_clt_bin}/ar" AND
       (NOT CMAKE_AR OR CMAKE_AR STREQUAL "/usr/bin/ar"))
        set(CMAKE_AR "${_iommu_clt_bin}/ar" CACHE FILEPATH
            "Path to ar" FORCE)
    endif()
    if(EXISTS "${_iommu_clt_bin}/ranlib" AND
       (NOT CMAKE_RANLIB OR CMAKE_RANLIB STREQUAL "/usr/bin/ranlib"))
        set(CMAKE_RANLIB "${_iommu_clt_bin}/ranlib" CACHE FILEPATH
            "Path to ranlib" FORCE)
    endif()
endif()

project(w27_f0778a2452 CXX)
set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

add_library(iommu_ats STATIC
    src/CanonicalScenario.cpp
    src/HostPagePool.cpp
    src/Interceptor.cpp
    src/Invalidation.cpp
    src/Machine.cpp
    src/PageTable.cpp
    src/ReferenceModel.cpp
    src/InterleavingSearch.cpp
    src/OrderVerifier.cpp
    src/Topology.cpp
    src/TranslationCache.cpp
)
target_include_directories(iommu_ats PUBLIC
    ${CMAKE_CURRENT_SOURCE_DIR}/include
)

if(NOT MSVC)
    target_compile_options(iommu_ats PRIVATE
        -Wall -Wextra -Wpedantic -Wconversion -Werror
    )
endif()

option(IOMMU_ATS_BUILD_TESTS "Build IOMMU/ATS model tests" ON)
if(IOMMU_ATS_BUILD_TESTS)
    enable_testing()
    add_executable(iommu_ats_tests tests/test_main.cpp)
    target_link_libraries(iommu_ats_tests PRIVATE iommu_ats)
    if(NOT MSVC)
        target_compile_options(iommu_ats_tests PRIVATE
            -Wall -Wextra -Wpedantic -Wconversion -Werror
        )
    endif()
    add_test(NAME iommu_ats_tests COMMAND iommu_ats_tests)
endif()

add_executable(iommu_ats_search_demo examples/search_demo.cpp)
target_link_libraries(iommu_ats_search_demo PRIVATE iommu_ats)
if(NOT MSVC)
    target_compile_options(iommu_ats_search_demo PRIVATE
        -Wall -Wextra -Wpedantic -Wconversion -Werror
    )
endif()
