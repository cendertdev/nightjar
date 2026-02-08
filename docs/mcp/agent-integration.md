---
layout: default
title: Agent Integration
parent: MCP Server
nav_order: 3
---

# Agent Integration Guide
{: .no_toc }

Connect AI coding assistants to Nightjar's MCP server.
{: .fs-6 .fw-300 }

## Table of contents
{: .no_toc .text-delta }

1. TOC
{:toc}

---

## Overview

Nightjar's MCP server enables AI assistants to help developers understand and resolve policy-related issues. This guide covers integration with popular AI tools.

---

## Claude Desktop

Claude Desktop can connect to MCP servers for enhanced capabilities.

### Configuration

Add Nightjar to your Claude Desktop configuration:

```json
{
  "mcpServers": {
    "nightjar": {
      "command": "kubectl",
      "args": [
        "exec", "-i", "-n", "nightjar-system",
        "deployment/nightjar-controller", "--",
        "/app/mcp-server"
      ]
    }
  }
}
```

Or with port forwarding:

```json
{
  "mcpServers": {
    "nightjar": {
      "url": "http://localhost:8090/mcp",
      "transport": "sse"
    }
  }
}
```

Start port forwarding before connecting:
```bash
kubectl port-forward -n nightjar-system svc/nightjar-controller 8090:8090
```

### Example Conversation

```
You: My deployment in the production namespace keeps failing
     with "connection refused to external-api.example.com:443"

Claude: I'll check what constraints might be affecting your deployment.

[Calls nightjar_explain]

Claude: I found the issue. There's a network policy called 'restrict-egress'
in the production namespace that only allows egress to specific endpoints.

The policy currently allows:
- DNS (kube-system, port 53)
- Internal services (*.svc.cluster.local)

It does not allow connections to external-api.example.com.

To resolve this, you have two options:

1. Request an exception from platform-team@company.com
2. Route through an egress proxy if available

Would you like me to help draft the exception request?
```

---

## VS Code with Copilot

GitHub Copilot can use MCP servers in VS Code.

### Setup

1. Install the MCP extension for VS Code
2. Add Nightjar server configuration:

```json
{
  "mcp.servers": {
    "nightjar": {
      "type": "sse",
      "url": "http://localhost:8090/mcp"
    }
  }
}
```

3. Start port forwarding:
```bash
kubectl port-forward -n nightjar-system svc/nightjar-controller 8090:8090
```

### Usage

In VS Code, ask Copilot about deployment issues:

```
@workspace Why is my deployment failing with admission webhook error?
```

Copilot will use Nightjar's MCP tools to diagnose the issue.

---

## Custom Agent Integration

Build your own agent integration using the MCP protocol.

### Python Client

```python
import httpx
import json

class NightjarClient:
    def __init__(self, base_url="http://localhost:8090"):
        self.base_url = base_url
        self.client = httpx.Client()

    def query(self, namespace, **filters):
        """Query constraints in a namespace."""
        params = {"namespace": namespace, **filters}
        response = self.client.post(
            f"{self.base_url}/tools/nightjar_query",
            json=params
        )
        return response.json()

    def explain(self, error_message, namespace, workload_name=None):
        """Explain an error message."""
        params = {
            "error_message": error_message,
            "namespace": namespace
        }
        if workload_name:
            params["workload_name"] = workload_name

        response = self.client.post(
            f"{self.base_url}/tools/nightjar_explain",
            json=params
        )
        return response.json()

    def check(self, manifest_yaml):
        """Pre-check a manifest."""
        response = self.client.post(
            f"{self.base_url}/tools/nightjar_check",
            json={"manifest": manifest_yaml}
        )
        return response.json()

    def get_health(self):
        """Get controller health."""
        response = self.client.get(
            f"{self.base_url}/resources/health"
        )
        return response.json()


# Usage
client = NightjarClient()

# Query constraints
result = client.query(
    namespace="production",
    constraint_type="NetworkEgress",
    severity="Critical"
)
print(f"Found {result['total']} constraints")

# Explain an error
explanation = client.explain(
    "connection refused to port 9090",
    namespace="my-app"
)
print(f"Confidence: {explanation['confidence']}")
print(f"Explanation: {explanation['explanation']}")
```

### TypeScript Client

```typescript
interface QueryResult {
  namespace: string;
  constraints: Constraint[];
  total: number;
}

interface ExplainResult {
  explanation: string;
  confidence: 'high' | 'medium' | 'low';
  matching_constraints: Constraint[];
  remediation_steps: RemediationStep[];
}

class NightjarClient {
  constructor(private baseUrl: string = 'http://localhost:8090') {}

  async query(
    namespace: string,
    filters?: {
      constraintType?: string;
      severity?: string;
      workloadName?: string;
    }
  ): Promise<QueryResult> {
    const response = await fetch(
      `${this.baseUrl}/tools/nightjar_query`,
      {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ namespace, ...filters })
      }
    );
    return response.json();
  }

  async explain(
    errorMessage: string,
    namespace: string,
    workloadName?: string
  ): Promise<ExplainResult> {
    const response = await fetch(
      `${this.baseUrl}/tools/nightjar_explain`,
      {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          error_message: errorMessage,
          namespace,
          workload_name: workloadName
        })
      }
    );
    return response.json();
  }
}

// Usage
const client = new NightjarClient();

const result = await client.explain(
  'connection refused to database:5432',
  'my-app'
);

if (result.confidence === 'high') {
  console.log('Found likely cause:', result.matching_constraints[0].name);
}
```

---

## In-Cluster Agent

For agents running inside the cluster, use Kubernetes ServiceAccount authentication.

### ServiceAccount Setup

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: my-agent
  namespace: my-app
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: my-agent-nightjar
subjects:
  - kind: ServiceAccount
    name: my-agent
    namespace: my-app
roleRef:
  kind: ClusterRole
  name: nightjar-mcp-client
  apiGroup: rbac.authorization.k8s.io
```

### Accessing MCP from Pod

```python
import os
import httpx

# Get ServiceAccount token
token_path = "/var/run/secrets/kubernetes.io/serviceaccount/token"
with open(token_path) as f:
    token = f.read()

# Connect to Nightjar
client = httpx.Client(
    base_url="http://nightjar-controller.nightjar-system.svc:8090",
    headers={"Authorization": f"Bearer {token}"}
)

# Make requests
response = client.post(
    "/tools/nightjar_query",
    json={"namespace": "my-app"}
)
```

---

## LangChain Integration

Use Nightjar as a LangChain tool.

```python
from langchain.tools import Tool
from langchain.agents import initialize_agent

class NightjarTools:
    def __init__(self, client):
        self.client = client

    def query_tool(self):
        return Tool(
            name="nightjar_query",
            description="Query Kubernetes constraints in a namespace",
            func=lambda input: self.client.query(
                namespace=input.get("namespace"),
                **input
            )
        )

    def explain_tool(self):
        return Tool(
            name="nightjar_explain",
            description="Explain a Kubernetes error by matching to constraints",
            func=lambda input: self.client.explain(
                error_message=input["error"],
                namespace=input["namespace"]
            )
        )

# Usage with LangChain agent
client = NightjarClient()
tools = NightjarTools(client)

agent = initialize_agent(
    tools=[tools.query_tool(), tools.explain_tool()],
    llm=your_llm,
    agent="zero-shot-react-description"
)

response = agent.run(
    "Why is my pod in the production namespace failing with 'connection refused'?"
)
```

---

## Troubleshooting

### Connection Refused

```bash
# Check MCP is enabled
kubectl get deployment -n nightjar-system nightjar-controller -o yaml | grep -A5 mcp

# Check pod is running
kubectl get pods -n nightjar-system -l app=nightjar-controller

# Check service
kubectl get svc -n nightjar-system nightjar-controller
```

### Authentication Errors

```bash
# Test with curl
kubectl exec -it -n nightjar-system deployment/nightjar-controller -- \
  curl -s http://localhost:8090/resources/health
```

### Empty Results

```bash
# Verify constraints exist
kubectl get constraintreports -A

# Check controller logs
kubectl logs -n nightjar-system -l app=nightjar-controller | grep -i mcp
```

---

## Best Practices

### For Agent Developers

1. **Handle confidence levels**: Don't present low-confidence matches with certainty
2. **Include remediation**: Always show remediation steps when available
3. **Respect privacy**: Don't cache or log cross-namespace details
4. **Rate limit requests**: Avoid overwhelming the MCP server

### For Platform Teams

1. **Enable MCP in production**: Set `mcp.enabled: true`
2. **Configure authentication**: Use `kubernetes-sa` for in-cluster agents
3. **Set remediation contacts**: Fill in `privacy.remediationContact`
4. **Monitor usage**: Check MCP request metrics

### Security Considerations

1. MCP respects RBAC—agents see only what their ServiceAccount allows
2. Cross-namespace constraint names are redacted by default
3. Sensitive policy details (Rego source, webhook URLs) are never exposed
4. Use network policies to restrict MCP access if needed
