.PHONY: all build test clean test-integration

# Binary name
BINARY_NAME=gobuildcache

# Build directory
BUILD_DIR=./builds

all: build test

# Build the cache program
build:
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p $(BUILD_DIR)
	go build -o $(BUILD_DIR)/$(BINARY_NAME) .

# Run tests with the cache program
test-manual: build
	@echo "Running tests with cache program..."
	GOCACHEPROG="$(shell pwd)/$(BUILD_DIR)/$(BINARY_NAME)" DEBUG=true go test -v ./tests

# Clean build artifacts
clean:
	@echo "Cleaning..."
	rm -f $(BUILD_DIR)/$(BINARY_NAME)
	rm -rf $(BUILD_DIR)/cache

# Run the cache server directly
run: build
	DEBUG=true $(BUILD_DIR)/$(BINARY_NAME)

# Clear the cache
clear: build
	DEBUG=true $(BUILD_DIR)/$(BINARY_NAME) clear

test:
	@echo "Running short tests..."
	go test -short -count 1 -v -race .

test-long:
	@echo "Running short and longer tests..."
	TEST_S3_BUCKET=test-go-build-cache AWS_ENDPOINT_URL_S3=https://t3.storage.dev AWS_ACCESS_KEY_ID=tid_GHaEn_WOoPpmoblCBaWQCWolCHEaZnRWKYYiGdpgWuuvEpUgaI AWS_SECRET_ACCESS_KEY=tsec_iT8vZcCZwmc17pmdovhN4YBx5k5KXUbdVfqbbMuYky+SBpK8mRlANt_0dR2O87+9I0HSfv AWS_REGION=auto go test -count 1 -v -race .

