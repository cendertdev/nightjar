---
layout: default
title: Quickstart
parent: Getting Started
nav_order: 2
---

# Quickstart
{: .no_toc }

A 5-minute hands-on introduction to Nightjar.
{: .fs-6 .fw-300 }

## Table of contents
{: .no_toc .text-delta }

1. TOC
{:toc}

---

## Prerequisites

- Kubernetes cluster (minikube, kind, or cloud)
- Helm 3.10+
- kubectl configured

---

## Step 1: Install Nightjar

```bash
# Add Helm repo
helm repo add nightjar https://nightjarctl.github.io/charts
helm repo update

# Install
helm install nightjar nightjar/nightjar \
  -n nightjar-system \
  --create-namespace

# Wait for pods
kubectl wait --for=condition=ready pod -l app=nightjar-controller \
  -n nightjar-system --timeout=120s
```

---

## Step 2: Create a Test Namespace with Policies

```bash
# Create namespace
kubectl create namespace quickstart-demo

# Create a NetworkPolicy that restricts egress
cat <<EOF | kubectl apply -f -
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: restrict-egress
  namespace: quickstart-demo
spec:
  podSelector: {}
  policyTypes:
    - Egress
  egress:
    - to:
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: kube-system
      ports:
        - port: 53
          protocol: UDP
    - to:
        - ipBlock:
            cidr: 0.0.0.0/0
      ports:
        - port: 443
          protocol: TCP
EOF

# Create a ResourceQuota
cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: ResourceQuota
metadata:
  name: compute-quota
  namespace: quickstart-demo
spec:
  hard:
    requests.cpu: "2"
    requests.memory: 2Gi
    limits.cpu: "4"
    limits.memory: 4Gi
EOF
```

---

## Step 3: View Discovered Constraints

Wait a few seconds for Nightjar to discover the policies, then check the ConstraintReport:

```bash
kubectl get constraintreport -n quickstart-demo
```

Output:
```
NAME          CONSTRAINTS   CRITICAL   WARNING   AGE
constraints   2             1          1         30s
```

View details:
```bash
kubectl get constraintreport constraints -n quickstart-demo -o yaml
```

You'll see both the NetworkPolicy and ResourceQuota represented as constraints.

---

## Step 4: Install and Use the CLI

```bash
# Install CLI
go install github.com/nightjarctl/nightjar/cmd/nightjarctl@latest

# Query constraints
nightjar query -n quickstart-demo
```

Output:
```
NAMESPACE        NAME             TYPE            SEVERITY   EFFECT
quickstart-demo  restrict-egress  NetworkEgress   Critical   deny
quickstart-demo  compute-quota    ResourceLimit   Warning    limit
```

---

## Step 5: Explain an Error

Simulate a network error and ask Nightjar to explain it:

```bash
nightjar explain -n quickstart-demo "connection timed out to port 9090"
```

Output:
```
Explanation: This error appears to be network-related. The following
             network policies may be blocking traffic.
Confidence:  high

Matching Constraints:
  NAME             TYPE           SEVERITY   EFFECT
  restrict-egress  NetworkEgress  Critical   deny

Remediation:
  1. [manual] Contact your platform team to request an egress exception
  2. [kubectl] kubectl annotate pod <pod-name> nightjar.io/egress-exception=true
```

---

## Step 6: Pre-Check a Deployment

Create a test manifest and check it before deploying:

```bash
cat <<EOF > test-deployment.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: my-app
  namespace: quickstart-demo
spec:
  replicas: 1
  selector:
    matchLabels:
      app: my-app
  template:
    metadata:
      labels:
        app: my-app
    spec:
      containers:
        - name: app
          image: nginx
          # Note: no resource limits specified
EOF

nightjar check -f test-deployment.yaml
```

Output:
```
Manifest: Deployment/my-app in namespace quickstart-demo

Would Block: false

Warnings:
  - compute-quota: Resource quotas apply. Add resource limits to avoid
    scheduling failures.
  - restrict-egress: Egress will be limited to port 443 only.
```

---

## Step 7: View Cluster Status

See an overview of constraints across all namespaces:

```bash
nightjar status
```

Output:
```
NAMESPACE         TOTAL   CRITICAL   WARNING   INFO
quickstart-demo   2       1          1         0
kube-system       0       0          0         0
---
Total: 2 constraints across 1 namespace
Critical: 1, Warning: 1, Info: 0
```

---

## Step 8: JSON Output for Automation

All CLI commands support JSON output for scripting:

```bash
nightjar query -n quickstart-demo -o json | jq '.constraints[].name'
```

Output:
```json
"restrict-egress"
"compute-quota"
```

---

## Cleanup

```bash
kubectl delete namespace quickstart-demo
helm uninstall nightjar -n nightjar-system
kubectl delete namespace nightjar-system
```

---

## What's Next?

- [CLI Reference](/nightjar/cli/) - Full command documentation
- [Controller Configuration](/nightjar/controller/configuration/) - Customize behavior
- [MCP Server](/nightjar/mcp/) - Integrate with AI coding assistants
- [CRDs](/nightjar/crds/) - Understand the data model
