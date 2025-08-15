# Chronos Scheduler Helm Chart

A Helm chart for deploying the Chronos Kubernetes scheduler plugin.

## Overview

The Chronos scheduler is a custom Kubernetes scheduler plugin that optimizes pod placement by scheduling workloads on nodes predicted to become available the soonest. This is particularly useful for batch workloads and time-bounded applications with predictable durations.

## Prerequisites

- Kubernetes 1.28+
- Helm 3.8+

## Installation

### Quick Start

```bash
# Add the chart repository (replace with your actual repo)
helm repo add chronos-kubernetes-scheduler https://your-org.github.io/chronos-kubernetes-scheduler

# Install the chart
helm install my-scheduler chronos-kubernetes-scheduler/chronos-kubernetes-scheduler
```

### Local Installation

```bash
# Install from local chart
helm install my-scheduler ./charts/chronos-kubernetes-scheduler

# Install with custom values
helm install my-scheduler ./charts/chronos-kubernetes-scheduler -f my-values.yaml
```

### Install in Different Namespace

```bash
# Create namespace and install
kubectl create namespace scheduler-system
helm install my-scheduler ./charts/chronos-kubernetes-scheduler -n scheduler-system
```

## Configuration

### Common Configuration Examples

#### Basic Configuration
```yaml
# values.yaml
scheduler:
  name: my-custom-scheduler
  
resources:
  requests:
    cpu: 200m
    memory: 256Mi
  limits:
    cpu: 1000m
    memory: 1Gi
```

#### High Availability Setup
```yaml
# values.yaml
replicaCount: 2

scheduler:
  leaderElection:
    enabled: true
    leaseDuration: 15s
    renewDeadline: 10s

podDisruptionBudget:
  enabled: true
  minAvailable: 1
```

#### Monitoring Enabled
```yaml
# values.yaml
metrics:
  enabled: true
  serviceMonitor:
    enabled: true
    interval: 30s
    labels:
      prometheus: kube-prometheus
```

#### Custom Plugin Configuration
```yaml
# values.yaml
scheduler:
  plugin:
    jobDurationAnnotation: "my-custom.io/duration"
    scoreMultiplier: 200
```

### Configuration Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `replicaCount` | int | `1` | Number of scheduler replicas |
| `image.registry` | string | `"ghcr.io"` | Image registry |
| `image.repository` | string | `"your-org/chronos-kubernetes-scheduler"` | Image repository |
| `image.tag` | string | `""` | Image tag (defaults to chart appVersion) |
| `image.pullPolicy` | string | `"IfNotPresent"` | Image pull policy |
| `scheduler.name` | string | `"chronos-kubernetes-scheduler"` | Scheduler name |
| `scheduler.plugin.name` | string | `"Chronos"` | Plugin name |
| `scheduler.plugin.jobDurationAnnotation` | string | `"scheduling.workload.io/expected-duration-seconds"` | Duration annotation key |
| `scheduler.plugin.scoreMultiplier` | int | `100` | Score multiplier for time vs capacity |
| `resources.requests.cpu` | string | `"100m"` | CPU request |
| `resources.requests.memory` | string | `"128Mi"` | Memory request |
| `resources.limits.cpu` | string | `"500m"` | CPU limit |
| `resources.limits.memory` | string | `"512Mi"` | Memory limit |
| `metrics.enabled` | bool | `true` | Enable metrics endpoint |
| `metrics.port` | int | `10259` | Metrics port |
| `serviceMonitor.enabled` | bool | `false` | Create ServiceMonitor for Prometheus |

For a complete list of configuration values, see [values.yaml](values.yaml).

## Usage

### Using the Scheduler in Your Pods

Once installed, use the scheduler by setting `schedulerName` in your pod specs:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: my-workload
  annotations:
    scheduling.workload.io/expected-duration-seconds: "300"  # 5 minutes
spec:
  schedulerName: chronos-kubernetes-scheduler  # Use your configured scheduler name
  containers:
  - name: worker
    image: my-app:latest
```

### Examples

The chart includes several example workloads in the `examples/` directory:

```bash
# Apply example workloads
kubectl apply -f examples/
```

## Testing

The chart includes built-in tests that can be run with Helm:

```bash
# Run chart tests
helm test my-scheduler

# Run tests with logs
helm test my-scheduler --logs
```

## Monitoring

### Prometheus Integration

Enable ServiceMonitor for Prometheus scraping:

```yaml
# values.yaml
metrics:
  enabled: true
  serviceMonitor:
    enabled: true
    labels:
      prometheus: kube-prometheus
```

### Key Metrics

The scheduler exposes several metrics:
- `scheduler_scheduling_duration_seconds` - Time spent scheduling
- `scheduler_pending_pods` - Number of pending pods
- `scheduler_scheduling_attempts_total` - Total scheduling attempts

### Grafana Dashboard

A sample Grafana dashboard is available in `monitoring/grafana-dashboard.json`.

## Troubleshooting

### Common Issues

#### Scheduler Not Starting
```bash
# Check pod status
kubectl get pods -l app.kubernetes.io/name=chronos-kubernetes-scheduler

# Check logs
kubectl logs -l app.kubernetes.io/name=chronos-kubernetes-scheduler
```

#### Pods Not Being Scheduled
```bash
# Check if scheduler is receiving pods
kubectl logs -l app.kubernetes.io/name=chronos-kubernetes-scheduler | grep "Scheduling pod"

# Check pod events
kubectl describe pod <pod-name>
```

#### Configuration Issues
```bash
# Check ConfigMap
kubectl get configmap <release-name>-chronos-kubernetes-scheduler-config -o yaml

# Validate configuration
helm template my-scheduler ./charts/chronos-kubernetes-scheduler --debug
```

### Debug Mode

Enable debug logging:

```yaml
# values.yaml
logging:
  level: 5  # Verbose logging (0-10)
```

## Upgrading

### Upgrade the Chart

```bash
# Upgrade to latest version
helm upgrade my-scheduler chronos-kubernetes-scheduler/chronos-kubernetes-scheduler

# Upgrade with new values
helm upgrade my-scheduler ./charts/chronos-kubernetes-scheduler -f new-values.yaml
```

### Migration Guide

#### From Plain YAML to Helm

If migrating from plain YAML manifests:

1. Uninstall existing deployment:
   ```bash
   kubectl delete -f deploy/manifests.yaml
   ```

2. Install Helm chart:
   ```bash
   helm install my-scheduler ./charts/chronos-kubernetes-scheduler
   ```

## Uninstalling

```bash
# Uninstall the chart
helm uninstall my-scheduler

# Clean up CRDs (if any)
kubectl delete crd <crd-name>
```

## Development

### Local Development

```bash
# Install chart locally
helm install dev ./charts/chronos-kubernetes-scheduler --set image.tag=dev

# Template and validate
helm template dev ./charts/chronos-kubernetes-scheduler --debug --dry-run
```

### Contributing

1. Make changes to chart templates
2. Update `Chart.yaml` version
3. Test with `helm template` and `helm test`
4. Update this README if needed

## Security Considerations

### RBAC

The chart creates minimal RBAC permissions required for scheduler operation:
- Read access to pods and nodes
- Write access to events
- Leader election permissions (if enabled)

### Security Context

Pods run with restrictive security context:
- Non-root user (65532)
- Read-only filesystem
- Dropped capabilities
- No privilege escalation

### Network Policy

Optional NetworkPolicy can be enabled to restrict network access:

```yaml
# values.yaml
networkPolicy:
  enabled: true
```

## License

This chart is licensed under the Apache License 2.0.
