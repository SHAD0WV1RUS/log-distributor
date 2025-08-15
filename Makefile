# Log Distributor Testing Platform
MAKEFLAGS += --no-print-directory
.PHONY: all build test clean help
.DEFAULT_GOAL := help

# Configuration
DOCKER_IMAGE := log-distributor
BUILD_MARKER := .build-marker
RESULTS_DIR := results
NETWORK_NAME := log-network

# File dependencies
GO_FILES := $(shell find . -name "*.go" -type f 2>/dev/null)
DOCKER_FILES := Dockerfile
CONFIG_FILES := go.mod go.sum
ALL_SRC_FILES := $(GO_FILES) $(DOCKER_FILES) $(CONFIG_FILES)

# Colors for output
RED := \033[0;31m
GREEN := \033[0;32m
YELLOW := \033[1;33m
BLUE := \033[0;34m
NC := \033[0m

# Test configurations
BASIC_CONFIG := EMITTERS=3 ANALYZERS=3 DURATION=60 RATE=300
THROUGHPUT_CONFIG := EMITTERS=50 ANALYZERS=10 DURATION=60 RATE=800
CHAOS_CONFIG := EMITTERS=20 ANALYZERS=8 DURATION=300 RATE=500 CHAOS_EVENTS=3

# Profiling configuration
PPROF_PORT := 6060
PPROF_ENABLED := $(if $(ENABLE_PPROF),true,false)

# Build system
$(BUILD_MARKER): $(ALL_SRC_FILES)
	@echo -e "$(BLUE)[BUILD]$(NC) Source files changed, rebuilding..."
	@docker build --target distributor -t $(DOCKER_IMAGE):distributor .
	@docker build --target analyzer -t $(DOCKER_IMAGE):analyzer .
	@docker build --target emitter -t $(DOCKER_IMAGE):emitter .
	@touch $(BUILD_MARKER)

build: $(BUILD_MARKER) ## Build Docker image if source files changed
	@echo -e "$(GREEN)[BUILD]$(NC) Docker image up to date"

force-build: ## Force rebuild regardless of file changes
	@echo -e "$(YELLOW)[BUILD]$(NC) Force rebuilding Docker image..."
	@docker build --no-cache --target distributor -t $(DOCKER_IMAGE):distributor .
	@docker build --no-cache --target analyzer -t $(DOCKER_IMAGE):analyzer .
	@docker build --no-cache --target emitter -t $(DOCKER_IMAGE):emitter .
	@touch $(BUILD_MARKER)

# Setup and cleanup
setup: ## Create required directories and network
	@mkdir -p $(RESULTS_DIR)
	@docker network create $(NETWORK_NAME) 2>/dev/null || true

clean: ## Clean up containers, volumes, and build artifacts
	@echo -e "$(YELLOW)[CLEANUP]$(NC) Stopping containers and cleaning up..."
	@$(MAKE) test-cleanup
	@docker network rm $(NETWORK_NAME) 2>/dev/null || true
	@docker system prune -f --volumes 2>/dev/null || true
	@rm -f $(BUILD_MARKER)

clean-logs: ## Clean only log files
	@echo -e "$(YELLOW)[CLEANUP]$(NC) Cleaning log files..."
	@rm -rf $(RESULTS_DIR)/*

clean-all: clean ## Complete cleanup including build artifacts
	@rm -rf $(RESULTS_DIR) $(BUILD_MARKER)

# Test suites
test: test-basic ## Run default test suite

test-basic: build setup ## Run basic functionality test
	@echo -e "$(BLUE)[TEST]$(NC) Running basic functionality test..."
	@$(MAKE) run-test TEST_NAME=basic $(BASIC_CONFIG)

test-throughput: build setup ## Run high throughput test
	@echo -e "$(BLUE)[TEST]$(NC) Running throughput test..."
	@$(MAKE) run-test TEST_NAME=throughput $(THROUGHPUT_CONFIG)

test-chaos: build setup ## Run chaos/resilience test
	@echo -e "$(BLUE)[TEST]$(NC) Running chaos test..."
	@$(MAKE) run-test TEST_NAME=chaos $(CHAOS_CONFIG) ENABLE_CHAOS=true



test-all: ## Run all test suites sequentially
	@echo -e "$(BLUE)[TEST]$(NC) Running complete test suite..."
	@$(MAKE) test-basic
	@$(MAKE) test-throughput
	@$(MAKE) test-chaos

# Core test runner
run-test: ## Internal target to run individual tests
	@echo -e "$(GREEN)[TEST:$(TEST_NAME)]$(NC) Starting test with $(EMITTERS)E/$(ANALYZERS)A, $(DURATION)s, $(RATE)msg/s"
	@$(MAKE) test-cleanup
	@$(MAKE) start-distributor
	@$(MAKE) start-analyzers
	@$(MAKE) start-emitters
	@$(MAKE) monitor-test
	@$(MAKE) collect-results
	@$(MAKE) test-cleanup

# Test orchestration
start-distributor:
	@echo -e "$(BLUE)[START]$(NC) Starting distributor..."
	@if [ "$(PPROF_ENABLED)" = "true" ]; then \
		echo -e "$(YELLOW)[PPROF]$(NC) Starting with profiling enabled on port $(PPROF_PORT)"; \
		docker run -d --name distributor \
			--network $(NETWORK_NAME) \
			-p "$(PPROF_PORT):$(PPROF_PORT)" \
			-e DISTRIBUTOR_PPROF_PORT="$(PPROF_PORT)" \
			$(DOCKER_IMAGE):distributor; \
	else \
		docker run -d --name distributor \
			--network $(NETWORK_NAME) \
			-e DISTRIBUTOR_PPROF_PORT="0" \
			$(DOCKER_IMAGE):distributor; \
	fi
	@echo "Waiting for distributor to be ready..."
	@timeout=30; while [ $$timeout -gt 0 ]; do \
		if docker ps --filter name=distributor --filter health=healthy | grep -q distributor; then break; fi; \
		sleep 1; timeout=$$((timeout-1)); \
	done; \
	if [ $$timeout -eq 0 ]; then \
		echo -e "$(RED)[ERROR]$(NC) Distributor failed to start"; exit 1; \
	fi

start-analyzers:
	@echo -e "$(BLUE)[START]$(NC) Starting $(ANALYZERS) analyzers with varied weights..."
	@for i in $$(seq 1 $(ANALYZERS)); do \
		weight=$$(echo "scale=3; 0.35 - ($$i - 1) * 0.05" | bc); \
		if [ $$(echo "$$weight < 0.05" | bc) -eq 1 ]; then weight="0.05"; fi; \
		echo "Starting analyzer-$$i with weight $$weight"; \
		docker run -d --name analyzer-$$i \
			--network $(NETWORK_NAME) \
			-e ANALYZER_WEIGHT="$$weight" \
			-e ANALYZER_VERBOSE="false" \
			-e ANALYZER_VALIDATE_CHECKSUMS="true" \
			-e ANALYZER_ID="analyzer-$$i" \
			$(DOCKER_IMAGE):analyzer; \
	done
	@echo "Waiting for analyzers to connect..."
	@sleep 8

start-emitters:
	@echo -e "$(BLUE)[START]$(NC) Starting $(EMITTERS) emitters..."
	@for i in $$(seq 1 $(EMITTERS)); do \
		docker run -d --name emitter-$$i \
			--network $(NETWORK_NAME) \
			-e EMITTER_RATE="$(RATE)" \
			-e EMITTER_DURATION="$(DURATION)" \
			$(DOCKER_IMAGE):emitter; \
	done

monitor-test:
	@echo -e "$(BLUE)[MONITOR]$(NC) Test running for $(DURATION) seconds..."
	@if [ "$(ENABLE_CHAOS)" = "true" ]; then $(MAKE) start-chaos-monitor & fi
	@if [ "$(PPROF_ENABLED)" = "true" ]; then $(MAKE) capture-pprof-profiles & fi
	@sleep $$(( $(DURATION) + 30 ))
	@echo -e "$(GREEN)[MONITOR]$(NC) Test completed"

start-chaos-monitor:
	@echo -e "$(YELLOW)[CHAOS]$(NC) Starting chaos testing..."
	@chaos_events=$${CHAOS_EVENTS:-3}; \
	chaos_interval=$$(( $(DURATION) / 9 )); \
	sleep 30; \
	for i in $$(seq 1 $$chaos_events); do \
		containers=$$(docker ps --filter "name=analyzer-" -q | head -1); \
		if [ -n "$$containers" ]; then \
			echo -e "$(YELLOW)[CHAOS $$i/$$chaos_events]$(NC) Killing analyzer container"; \
			docker kill $$containers 2>/dev/null || true; \
			sleep $$chaos_interval; \
			echo -e "$(BLUE)[CHAOS]$(NC) Restarting analyzer"; \
			container_name=$$(docker ps -a --filter "name=analyzer-" --format "{{.Names}}" | head -1); \
			if [ -n "$$container_name" ]; then \
				docker restart $$container_name 2>/dev/null || true; \
			fi; \
			if [ $$i -lt $$chaos_events ]; then sleep $$chaos_interval; fi; \
		fi; \
	done

collect-results:
	@echo -e "$(BLUE)[RESULTS]$(NC) Collecting test results..."
	@mkdir -p $(RESULTS_DIR)
	@echo "Sending graceful shutdown to analyzers..."
	@for i in $$(seq 1 $(ANALYZERS)); do \
		docker kill -s SIGTERM analyzer-$$i 2>/dev/null || true; \
	done
	@sleep 3
	@echo "Saving container logs..."
	@docker logs distributor > $(RESULTS_DIR)/distributor-$(TEST_NAME).log 2>&1
	@for i in $$(seq 1 $(ANALYZERS)); do \
		docker logs analyzer-$$i 2>&1 || true; \
	done > $(RESULTS_DIR)/analyzers-$(TEST_NAME).log
	@for i in $$(seq 1 $(EMITTERS)); do \
		docker logs emitter-$$i 2>&1 || true; \
	done > $(RESULTS_DIR)/emitters-$(TEST_NAME).log
	@$(MAKE) analyze-results

analyze-results:
	@echo -e "$(BLUE)[ANALYSIS]$(NC) Analyzing test results..."
	@$(MAKE) analyze-weight-distribution
	@$(MAKE) export-weight-routing-csv
	@$(MAKE) analyze-message-flow

analyze-weight-distribution:
	@echo -e "$(BLUE)[ANALYSIS]$(NC) Analyzing weight distribution..."
	@if [ -f $(RESULTS_DIR)/analyzers-$(TEST_NAME).log ]; then \
		echo ""; \
		echo "Weight Distribution Effectiveness:"; \
		temp_file=$$(mktemp); \
		grep "Per-second stats:" $(RESULTS_DIR)/analyzers-$(TEST_NAME).log | \
		awk '{ \
			timestamp = $$1 " " $$2; \
			match($$0, /([0-9]+) msg\/s/, rate_match); \
			rate = rate_match[1]; \
			match($$0, /weight: ([0-9.]+)/, weight_match); \
			weight = weight_match[1]; \
			match($$0, /analyzer: ([^)]+)/, analyzer_match); \
			analyzer = analyzer_match[1]; \
			print timestamp, analyzer, weight, rate \
		}' | sort > $$temp_file; \
		awk '{ \
			time = $$1 " " $$2; \
			analyzer = $$3; \
			weight = $$4; \
			rate = $$5; \
			timeline[time][analyzer] = weight; \
			rates[time][analyzer] = rate; \
		} END { \
			for (time in timeline) { \
				total_weight = 0; \
				total_rate = 0; \
				for (analyzer in timeline[time]) { \
					total_weight += timeline[time][analyzer]; \
					total_rate += rates[time][analyzer]; \
				} \
				if (total_weight > 0 && total_rate > 0) { \
					for (analyzer in timeline[time]) { \
						weight = timeline[time][analyzer]; \
						rate = rates[time][analyzer]; \
						expected_pct = (weight / total_weight) * 100; \
						actual_pct = (rate / total_rate) * 100; \
						deviation = actual_pct - expected_pct; \
						if (deviation < 0) deviation = -deviation; \
						if (expected_pct > 0) deviation /= expected_pct; \
						deviations[analyzer] += deviation; \
						deviation_counts[analyzer]++; \
					} \
				} \
			} \
			printf "%-25s %-12s %-12s\n", "Analyzer", "Avg Dev%", "Samples"; \
			printf "%-25s %-12s %-12s\n", "--------", "--------", "-------"; \
			for (analyzer in deviations) { \
				if (deviation_counts[analyzer] > 0) { \
					avg_dev = deviations[analyzer] / deviation_counts[analyzer]; \
					printf "%-25s %-12.1f %-12d\n", analyzer, avg_dev, deviation_counts[analyzer]; \
				} \
			} \
		}' $$temp_file; \
		rm -f $$temp_file; \
	else \
		echo "No analyzer logs found"; \
	fi

export-weight-routing-csv:
	@echo -e "$(BLUE)[EXPORT]$(NC) Exporting weight routing data to CSV (pivoted format)..."
	@if [ -f $(RESULTS_DIR)/analyzers-$(TEST_NAME).log ]; then \
		csv_file="$(RESULTS_DIR)/weight-routing-$(TEST_NAME).csv"; \
		temp_file=$$(mktemp); \
		grep "Per-second stats:" $(RESULTS_DIR)/analyzers-$(TEST_NAME).log | \
		awk '{ \
			timestamp = $$1 " " $$2; \
			match($$0, /([0-9]+) msg\/s/, rate_match); \
			rate = rate_match[1]; \
			match($$0, /weight: ([0-9.]+)/, weight_match); \
			weight = weight_match[1]; \
			match($$0, /analyzer: ([^)]+)/, analyzer_match); \
			analyzer = analyzer_match[1]; \
			print timestamp, analyzer, weight, rate \
		}' | sort > $$temp_file; \
		awk '{ \
			time = $$1 " " $$2; \
			analyzer = $$3; \
			weight = $$4; \
			rate = $$5; \
			timeline[time][analyzer] = weight; \
			rates[time][analyzer] = rate; \
			all_times[time] = 1; \
			all_analyzers[analyzer] = 1; \
		} END { \
			n_analyzers = 0; \
			for (analyzer in all_analyzers) { \
				sorted_analyzers[++n_analyzers] = analyzer; \
			} \
			asort(sorted_analyzers); \
			printf "timestamp,total_weight,total_rate"; \
			for (i = 1; i <= n_analyzers; i++) { \
				analyzer = sorted_analyzers[i]; \
				gsub(/analyzer_/, "", analyzer); \
				printf ",%s_weight,%s_rate,%s_expected_pct,%s_actual_pct,%s_deviation", \
					analyzer, analyzer, analyzer, analyzer, analyzer; \
			} \
			printf "\n"; \
			for (time in all_times) { \
				total_weight = 0; \
				total_rate = 0; \
				for (analyzer in timeline[time]) { \
					total_weight += timeline[time][analyzer]; \
					total_rate += rates[time][analyzer]; \
				} \
				if (total_weight > 0 && total_rate > 0) { \
					printf "%s,%.3f,%d", time, total_weight, total_rate; \
					for (i = 1; i <= n_analyzers; i++) { \
						analyzer = sorted_analyzers[i]; \
						if (analyzer in timeline[time]) { \
							weight = timeline[time][analyzer]; \
							rate = rates[time][analyzer]; \
							expected_pct = (weight / total_weight) * 100; \
							actual_pct = (rate / total_rate) * 100; \
							deviation = actual_pct - expected_pct; \
							deviation_abs = (deviation < 0) ? -deviation : deviation; \
							printf ",%.3f,%d,%.2f,%.2f,%.2f", \
								weight, rate, expected_pct, actual_pct, deviation_abs; \
						} else { \
							printf ",,,,,"; \
						} \
					} \
					printf "\n"; \
				} \
			} \
		}' $$temp_file > $$csv_file; \
		echo "CSV exported to: $$csv_file"; \
		echo "Records exported: $$(wc -l < $$csv_file | xargs expr -1 +)"; \
		echo ""; \
		rm -f $$temp_file; \
	else \
		echo "No analyzer logs found for CSV export"; \
	fi

analyze-message-flow:
	@echo -e "$(BLUE)[ANALYSIS]$(NC) Analyzing message flow..."
	@sent=$$(grep "Completed: sent.*messages" $(RESULTS_DIR)/emitters-$(TEST_NAME).log 2>/dev/null | \
		awk '{sum += $$5} END {print sum+0}'); \
	received=$$(grep "processed.*messages" $(RESULTS_DIR)/analyzers-$(TEST_NAME).log 2>/dev/null | \
		awk '{sum += $$6} END {print sum+0}'); \
	invalid=$$(grep "invalid checksums:" $(RESULTS_DIR)/analyzers-$(TEST_NAME).log 2>/dev/null | \
		awk '{sum += $$NF} END {print sum+0}'); \
	throughput=$$(echo "scale=2; $$sent / $(DURATION)" | bc 2>/dev/null || echo "0"); \
	echo ""; \
	echo -e "$(NC)=== TEST RESULTS ($(TEST_NAME)) ==="; \
	echo "Configuration: $(EMITTERS)E/$(ANALYZERS)A, $(DURATION)s"; \
	echo "Messages Sent: $$sent"; \
	echo "Messages Received: $$received"; \
	echo "Message Loss: $$((sent - received))"; \
	echo "Invalid Checksums: $$invalid"; \
	echo "Throughput: $$throughput msg/s"; \
	echo "Per-Emitter Rate: $$(echo "scale=2; $$throughput / $(EMITTERS)" | bc 2>/dev/null || echo "0") msg/s"; \
	echo ""; \
	if [ $$invalid -eq 0 ]; then \
		echo -e "$(GREEN)[SUCCESS]$(NC) All checksums valid"; \
	else \
		echo -e "$(YELLOW)[WARN]$(NC) $$invalid invalid checksums"; \
	fi; \
	if [ $$((sent - received)) -eq 0 ]; then \
		echo -e "$(GREEN)[SUCCESS]$(NC) No message loss"; \
	else \
		echo -e "$(YELLOW)[WARN]$(NC) $$((sent - received)) messages lost"; \
	fi

test-cleanup:
	@echo -e "$(YELLOW)[CLEANUP]$(NC) Cleaning up test containers..."
	@docker rm -f distributor 2>/dev/null || true
	@for i in $$(seq 1 20); do \
		docker rm -f analyzer-$$i 2>/dev/null || true; \
		docker rm -f emitter-$$i 2>/dev/null || true; \
	done


# Utility targets
status: ## Show system status
	@echo -e "$(BLUE)=== BUILD STATUS ===$(NC)"
	@if [ -f $(BUILD_MARKER) ]; then \
		echo "✓ Build marker exists ($(BUILD_MARKER))"; \
		echo "  Last build: $$(stat -c %y $(BUILD_MARKER) 2>/dev/null || echo 'unknown')"; \
	else \
		echo "✗ No build marker - build needed"; \
	fi
	@echo ""
	@echo -e "$(BLUE)=== CONTAINER STATUS ===$(NC)"
	@docker ps --filter "name=distributor" --filter "name=analyzer-" --filter "name=emitter-" 2>/dev/null || echo "No containers running"
	@echo ""
	@echo -e "$(BLUE)=== IMAGE STATUS ===$(NC)"
	@docker images $(DOCKER_IMAGE) 2>/dev/null | head -4 || echo "No images found"

logs: ## Show live logs from all services
	@echo "Use 'make logs-distributor', 'make logs-analyzers', or docker logs <container_name> for specific logs"

logs-distributor: ## Show distributor logs
	@docker logs -f distributor 2>/dev/null || echo "Distributor not running"

logs-analyzers: ## Show analyzer logs
	@for i in $$(seq 1 10); do docker logs analyzer-$$i 2>/dev/null || true; done

debug: ## Debug current test state
	@echo -e "$(BLUE)=== DEBUG INFO ===$(NC)"
	@echo "Active containers:"
	@docker ps --format "table {{.Names}}\t{{.Status}}\t{{.Ports}}"
	@echo ""
	@echo "Recent distributor logs:"
	@docker logs --tail=10 distributor 2>/dev/null || echo "No distributor logs"

# Profiling support
capture-pprof-profiles:
	@echo -e "$(BLUE)[PPROF]$(NC) Capturing profiles during test..."
	@mkdir -p $(RESULTS_DIR)
	@sleep 20  # Wait for test to stabilize
	@echo -e "$(BLUE)[PPROF]$(NC) Capturing heap profile..."
	@curl -s "http://localhost:$(PPROF_PORT)/debug/pprof/heap" > $(RESULTS_DIR)/heap-profile-$(TEST_NAME).pb.gz 2>/dev/null || true
	@echo -e "$(BLUE)[PPROF]$(NC) Capturing goroutine profile..."
	@curl -s "http://localhost:$(PPROF_PORT)/debug/pprof/goroutine" > $(RESULTS_DIR)/goroutine-profile-$(TEST_NAME).pb.gz 2>/dev/null || true
	@echo -e "$(BLUE)[PPROF]$(NC) Capturing CPU profile (30 seconds)..."
	@curl -s "http://localhost:$(PPROF_PORT)/debug/pprof/profile?seconds=30" > $(RESULTS_DIR)/cpu-profile-$(TEST_NAME).pb.gz 2>/dev/null || true
	@echo -e "$(GREEN)[PPROF]$(NC) Profiles saved to $(RESULTS_DIR)/ with prefix $(TEST_NAME)"
	@echo "Analyze with: go tool pprof $(RESULTS_DIR)/<profile-name>"

help: ## Show this help
	@echo "Log Distributor Testing Platform"
	@echo ""
	@echo "Usage: make [target]"
	@echo ""
	@echo "Build Targets:"
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## .*Build/ {printf "  %-20s %s\n", $$1, $$2}' $(MAKEFILE_LIST)
	@echo ""
	@echo "Test Targets:"
	@awk 'BEGIN {FS = ":.*?## "} /^test-.*:.*?## / {printf "  %-20s %s\n", $$1, $$2}' $(MAKEFILE_LIST)
	@echo ""
	@echo "Utility Targets:"
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / && !/Build/ && !/test-/ && !/^test:/ {printf "  %-20s %s\n", $$1, $$2}' $(MAKEFILE_LIST)
	@echo ""
	@echo "Examples:"
	@echo "  make test              # Run basic test"
	@echo "  make test-throughput   # Run high-load test"  
	@echo "  make test-all          # Run complete suite"
	@echo "  make clean             # Clean up everything"
	@echo ""
	@echo "Profiling:"
	@echo "  ENABLE_PPROF=1 make test-basic    # Run test with profiling"
	@echo "  ENABLE_PPROF=1 make test-chaos    # Run chaos test with profiling"
	@echo "  # Profiles saved to results/ directory"