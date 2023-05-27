# multicluster-cloud-provider

This repository defines the shared interfaces which Karmada cloud providers implement. These interfaces allow various 
controllers to integrate with any cloud provider in a pluggable fashion.

## Background

To enable Karmada to run on the public cloud platform and flexibly use and manage other basic resources and services on 
the cloud, cloud providers need to implement their own adapters. However, in the implementation process, some works are 
the same for each cloud provider.

Learn from the experience of Kubernetes Cloud Controller Manager (CCM): https://github.com/kubernetes/cloud-provider. 
Karmada can also provide a public repository that provides interfaces for using and managing basic resources and services 
on the public cloud. Cloud providers only need to implement these interfaces to provide users with their own adapters.

## Purpose

This library is shared dependency for processes which need to be able to integrate with cloud provider specific functionality.

## Fake testing

### Command:

Make multicluster-provider-fake binary:
```shell
make multicluster-provider-fake
```

Make multicluster-provider-fake image:
```shell
make image-multicluster-provider-fake
```

Deploy multicluster-provider-fake deployment:
```shell
hack/deploy-provider.sh
```

Delete multicluster-provider-fake deployment :
```shell
kubectl --context karmada-host -n karmada-system delete deployments.apps multicluster-provider-fake
```

### Verify

<details>
<summary>mci.yaml</summary>

```yaml
apiVersion: networking.karmada.io/v1alpha1
kind: MultiClusterIngress
metadata:
  name: minimal-ingress
  annotations:
    nginx.ingress.kubernetes.io/rewrite-target: /
spec:
  rules:
  - http:
      paths:
      - path: /testpath
        pathType: Prefix
        backend:
          service:
            name: serve
            port:
              number: 80
```
</details>

<details>
<summary>application.yaml</summary>

```
apiVersion: apps/v1
kind: Deployment
metadata:
  name: serve
spec:
  replicas: 1
  selector:
    matchLabels:
      app: serve
  template:
    metadata:
      labels:
        app: serve
    spec:
      containers:
      - name: serve
        image: jeremyot/serve:0a40de8
        args:
        - "--message='hello from cluster member1 (Node: {{env \"NODE_NAME\"}} Pod: {{env \"POD_NAME\"}} Address: {{addr}})'"
        env:
          - name: NODE_NAME
            valueFrom:
              fieldRef:
                fieldPath: spec.nodeName
          - name: POD_NAME
            valueFrom:
              fieldRef:
                fieldPath: metadata.name
---
apiVersion: v1
kind: Service
metadata:
  name: serve
spec:
  ports:
  - port: 80
    targetPort: 8080
  selector:
    app: serve
---
apiVersion: policy.karmada.io/v1alpha1
kind: PropagationPolicy
metadata:
  name: mcs-workload
spec:
  resourceSelectors:
    - apiVersion: apps/v1
      kind: Deployment
      name: serve
    - apiVersion: v1
      kind: Service
      name: serve
  placement:
    clusterAffinity:
      clusterNames:
        - member1
        - member2
    replicaScheduling:
      replicaDivisionPreference: Weighted
      replicaSchedulingType: Divided
      weightPreference:
        staticWeightList:
          - targetCluster:
              clusterNames:
                - member1
            weight: 1
          - targetCluster:
              clusterNames:
                - member2
            weight: 1
```
</details>

```shell
kubectl apply -f mci.yaml
kubectl apply -f application.yaml
```