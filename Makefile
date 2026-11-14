CXX ?= clang++
CXXSTD ?= -std=c++20
# Apple Clang requires -fcxx-modules to accept C++20 named module syntax.
MODULES ?= -fcxx-modules
WARNINGS := -Wall -Wextra -Wpedantic
CXXFLAGS := $(CXXSTD) $(MODULES) $(WARNINGS)

BUILD := build
PCM := $(BUILD)/netsec.onboarding.pcm
IMPL_OBJ := $(BUILD)/onboarding.impl.o
TEST_BIN := $(BUILD)/onboarding_test

.PHONY: all test clean

all: test

$(BUILD):
	mkdir -p $(BUILD)

# Precompile the module interface unit into a binary module interface.
$(PCM): src/onboarding.cppm | $(BUILD)
	$(CXX) $(CXXFLAGS) -x c++-module --precompile $< -o $@

# Compile the implementation unit against the precompiled interface.
$(IMPL_OBJ): src/onboarding.impl.cpp $(PCM) | $(BUILD)
	$(CXX) $(CXXFLAGS) -fmodule-file=netsec.onboarding=$(PCM) -c $< -o $@

# Build the test consumer, which imports the module by name.
$(TEST_BIN): tests/onboarding_test.cpp $(IMPL_OBJ) $(PCM) | $(BUILD)
	$(CXX) $(CXXFLAGS) -fmodule-file=netsec.onboarding=$(PCM) \
		tests/onboarding_test.cpp $(IMPL_OBJ) -o $@

test: $(TEST_BIN)
	./$(TEST_BIN)

clean:
	rm -rf $(BUILD)
