# Scheduler simulations

The `queuesort_max_queue_age` scenario verifies max-age promotion followed by
normal longest-duration-first ordering for the remaining young pods.

Run the complete test from the repository root:

```bash
kind create cluster \
  --name chronos-test \
  --config test-workloads/chronos-kind.yaml
kind get kubeconfig --name chronos-test > /tmp/chronos-test-kubeconfig

docker build \
  -f build/Dockerfile \
  -t localhost/chronos-kubernetes-scheduler:integration-test \
  --load .
kind load docker-image \
  localhost/chronos-kubernetes-scheduler:integration-test \
  --name chronos-test

helm upgrade --install chronos-scheduler \
  charts/chronos-kubernetes-scheduler \
  --namespace chronos-system \
  --create-namespace \
  --set replicaCount=1 \
  --set image.registry=localhost \
  --set image.repository=chronos-kubernetes-scheduler \
  --set image.tag=integration-test \
  --set image.pullPolicy=Never \
  --set scheduler.profileName=chronos-optimized-queue-sort-reserve \
  --set scheduler.leaderElection.enabled=false \
  --set logging.level=6 \
  --set resources.requests.cpu=100m \
  --set resources.requests.memory=128Mi \
  --set resources.limits.cpu=500m \
  --set resources.limits.memory=512Mi \
  --values test-workloads/max-queue-age-values.yaml \
  --wait
```

Then run the scenario:

```bash
python3 test-workloads/run-simulations.py \
  --config test-workloads/simulations.yaml \
  --kubeconfig /tmp/chronos-test-kubeconfig \
  --scenario queuesort_max_queue_age
```

The simulation verifies that the scheduler loaded `maxQueueAge: 1m`, taints
`chronos-test-worker`, and follows this creation timeline:

- At `t=1s`, create `pod1` with a 10-second expected duration.
- At `t=30s`, create `pod2` with a 20-second expected duration.
- At `t=40s`, create `pod3` with a 30-second expected duration.
- At `t=70s`, remove the taint.

At `t=70s`, only `pod1` exceeds the one-minute queue age. It is scheduled
first, after which the two young pods retain longest-duration-first ordering:

1. `pod1`
2. `pod3`
3. `pod2`
