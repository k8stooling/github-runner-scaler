# GitHub Runner Scaler API

This is a simple API to count the number of queued GitHub Actions jobs for all repositories in a GitHub organization. The API is intended to be used as a KEDA scaler to adjust the number of GitHub runners dynamically based on the job queue length.

## Features
Fetches and counts the number of queued jobs across all repositories in a GitHub organization.
Caches the result for a configurable amount of time to reduce the number of API calls to GitHub.
Supports both GitHub Enterprise and public GitHub by automatically adjusting API URLs.
Exposes an HTTP endpoint to return the queued jobs count in JSON format.

## Installation

In kubernetes style:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: github-runner-scaler
  labels:
    app: github-runner-scaler
spec:
  replicas: 1
  selector:
    matchLabels:
      app: github-runner-scaler
  template:
    metadata:
      labels:
        app: github-runner-scaler
    spec:
      containers:
      - name: scaler
        image: ghcr.io/k8stooling/github-runner-scaler:latest
        env:
        - name: GITHUB_URL
          valueFrom:
            secretKeyRef:
              name: github-secrets
              key: GITHUB_URL
        - name: GITHUB_ORGANIZATION
          valueFrom:
            secretKeyRef:
              name: github-secrets
              key: GITHUB_ORGANIZATION
        - name: GITHUB_TOKEN
          valueFrom:
            secretKeyRef:
              name: github-secrets
              key: GITHUB_TOKEN
        - name: GITHUB_RUNNER_SCALER_CACHE_TIMEOUT
          value: "60"  # Cache timeout in seconds
        ports:
        - containerPort: 8080
---
apiVersion: v1
kind: Service
metadata:
  name: github-runner-scaler
  labels:
    app: github-runner-scaler
spec:
  selector:
    app: github-runner-scaler
  ports:
    - protocol: TCP
      port: 8080
      targetPort: 8080
  type: ClusterIP
```

## Example Request
```bash
Copy code
curl http://localhost:8080/queued_jobs
Example Response:
json
Copy code
{
  "queued_jobs": 5
}
```

## Environment Variables

- GITHUB_URL: The base URL for the GitHub API.
  Default: https://api.github.com
  Example: https://your-github-enterprise-instance/api/v3
- GITHUB_ORGANIZATION: The GitHub organization name.
  Example: your-org-name
- GITHUB_TOKEN: A personal access token with necessary permissions.
  Permissions: repo, workflow
- GITHUB_RUNNER_SCALER_CACHE_TIMEOUT: Cache duration in seconds.
  Default: 60 seconds
- PORT: The port the server will listen on.
  Default: 8080
