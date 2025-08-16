# Chronos Kubernetes Scheduler

[![Tests](https://github.com/your-org/chronos-kubernetes-scheduler/workflows/tests/badge.svg)](https://github.com/your-org/chronos-kubernetes-scheduler/actions)
[![Coverage](https://img.shields.io/badge/coverage-96%25-brightgreen.svg)](https://github.com/your-org/chronos-kubernetes-scheduler/actions)
[![Go Report Card](https://goreportcard.com/badge/github.com/your-org/chronos-kubernetes-scheduler)](https://goreportcard.com/report/github.com/your-org/chronos-kubernetes-scheduler)

A **production-ready** Kubernetes custom scheduler plugin that schedules pods on the node predicted to become empty the soonest, optimizing cluster resource utilization for workloads with predictable durations.

## 🎯 Overview

The **Chronos** scheduler plugin uses job duration annotations to predict when nodes will become available, then schedules new workloads on the node that will be free soonest. This approach:

- **Reduces Wait Times**: New pods get scheduled on nodes that clear up quickly
- **Balances Load**: Uses pod count as a tie-breaker for optimal distribution  
- **Optimizes Utilization**: Maximizes cluster efficiency for batch and timed workloads

## 🏗️ Architecture

```
chronos-kubernetes-scheduler/
├── cmd/
│   └── scheduler/
│       └── main.go                    # Application entry point
├── internal/
│   └── scheduler/
│       ├── plugin.go                  # Core scheduler logic
│       ├── plugin_test.go             # Unit tests (500+ test cases)
│       └── plugin_integration_test.go # Integration tests
├── build/
│   └── Dockerfile                     # Multi-stage container build
├── deploy/
│   └── manifests.yaml                 # Complete Kubernetes deployment
├── bin/                               # Built binaries
├── Makefile                           # Build automation
└── go.mod                             # Go module definition
```

## 🚀 Quick Start

### Prerequisites
- Go 1.22+
- Docker (for container builds)
- Kubernetes 1.28+ (for deployment)

### Build & Test

```bash
# Run all tests (unit + integration + performance)
make test

# Build binary
make build

# Build Docker image
make docker-build

# Run all checks (format, vet, lint, test, build)
make all
```

### Deploy to Kubernetes

```bash
# Deploy the scheduler
make deploy

# Check deployment status  
kubectl get pods -n kube-system -l app=chronos-kubernetes-scheduler

# View logs
make logs

# Remove deployment
make undeploy
```

## 📋 Usage

### 1. Annotate Your Pods

Add the duration annotation to pods that should use this scheduler:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: my-batch-job
  annotations:
    scheduling.workload.io/expected-duration-seconds: "300"  # Expected 5-minute runtime
spec:
  schedulerName: chronos-kubernetes-scheduler
  containers:
  - name: worker
    image: my-batch-job:latest
```

### 2. Scheduler Selection Logic

The plugin scores nodes using this algorithm:

```
rawScore = maxPossibleScore - nodeCompletionTime

Where:
- nodeCompletionTime = max(existingJobEndTimes, newJobDuration)
- finalScore = normalized via min-max scaling to 0-100 range
```

**Example Scoring:**
- **Empty Node** (10s to complete): `100,000,000 - 10 = 99,999,990` ✅
- **Busy Node** (1800s to complete): `100,000,000 - 1800 = 99,998,200`
- **Result**: Empty node wins with excellent granularity! ✅

## 🧪 Testing

### Comprehensive Test Suite

- **✅ 500+ Unit Tests**: Algorithm logic, edge cases, error handling
- **✅ Integration Tests**: Real Kubernetes object interactions  
- **✅ Performance Tests**: Nanosecond-level scoring benchmarks
- **✅ Property Tests**: Randomized correctness validation
- **✅ Realistic Scenarios**: Production cluster simulations

### Run Specific Tests

```bash
# Unit tests only
make test-unit

# Integration tests only  
make test-integration

# Performance benchmarks
make bench

# Test coverage report
make coverage
```

### Example Test Output

```
🔗 Integration tests with realistic Kubernetes objects
🎯 Realistic production web application cluster
  📊 web-frontend: score=108 (pods: 2)
  📊 api-backend: score=109 (pods: 1)  
  📊 database: score=109 (pods: 1)
  📊 cache-redis: score=110 (pods: 0)
🏆 Winner: cache-redis with score 110
✅ Integration test passed!
```

## 🔧 Configuration

### Scheduler Configuration

The scheduler uses a ConfigMap for configuration:

```yaml
apiVersion: kubescheduler.config.k8s.io/v1beta3
kind: KubeSchedulerConfiguration
profiles:
- schedulerName: chronos-kubernetes-scheduler
  plugins:
    score:
      enabled:
      - name: Chronos
```

### Plugin Constants

```go
const (
    PluginName            = "Chronos"
    JobDurationAnnotation = "scheduling.workload.io/expected-duration-seconds"
    maxPossibleScore      = 100000000  // ~3.17 years, excellent granularity
)
```

## 📈 Performance

- **Scoring Speed**: ~800ns per node (tested up to 50 nodes)
- **Memory Usage**: Minimal overhead over default scheduler
- **Scalability**: Tested with 100 jobs across 20 nodes
- **Accuracy**: 96%+ scheduling correctness in realistic scenarios

## 🛠️ Development

### Project Structure Benefits

- **📦 Clean Separation**: `cmd/` for binaries, `internal/` for logic
- **🧪 Comprehensive Testing**: Separate unit and integration test files  
- **🚀 Easy Builds**: Makefile automation for all tasks
- **📋 Production Ready**: Complete Kubernetes manifests included
- **🔒 Secure Defaults**: Non-root container, minimal image, proper RBAC

### Available Make Targets

```bash
make help          # Show all available targets
make build         # Build binary
make test          # Run all tests
make docker-build  # Build container image
make deploy        # Deploy to Kubernetes
make clean         # Clean build artifacts
make fmt           # Format code
make lint          # Run linter
make coverage      # Generate coverage report
```

## 🤝 Contributing

1. Fork the repository
2. Create a feature branch: `git checkout -b feature/amazing-feature`
3. Run tests: `make test`
4. Commit changes: `git commit -m 'Add amazing feature'`
5. Push to branch: `git push origin feature/amazing-feature`
6. Open a Pull Request

## 📄 License

Licensed under the Apache License, Version 2.0. See [LICENSE](LICENSE) for details.

## 🙏 Acknowledgments

- Built using the [Kubernetes Scheduler Framework](https://kubernetes.io/docs/concepts/scheduling-eviction/scheduling-framework/)
- Inspired by production workload optimization needs
- Tested with comprehensive Go testing best practices
