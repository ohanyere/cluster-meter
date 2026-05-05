# Cluster Meter

## GitOps Deployment with ArgoCD

Cluster Meter can be deployed from Git by ArgoCD using the manifests in `deploy/argocd/`.
The ArgoCD `Application` syncs the Kubernetes manifests from the `deploy` path on the
`main` branch and deploys them into the `cluster-meter` namespace.

Apply the ArgoCD project and application after ArgoCD is installed in the EKS cluster:

```bash
kubectl apply -f deploy/argocd/project.yaml
kubectl apply -f deploy/argocd/application.yaml
kubectl get applications -n argocd
kubectl describe application cluster-meter -n argocd
```

The application uses the published Docker Hub image:

```text
docker.io/ohanyere/cluster-meter:latest
```
