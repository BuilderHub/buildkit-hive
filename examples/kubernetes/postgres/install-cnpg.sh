#!/usr/bin/env bash
# Install the CloudNativePG operator (cluster-scoped). Safe to re-run.
set -euo pipefail

CNPG_VERSION="${CNPG_VERSION:-1.25.0}"
MANIFEST_URL="https://raw.githubusercontent.com/cloudnative-pg/cloudnative-pg/release-1.25/releases/cnpg-${CNPG_VERSION}.yaml"

if kubectl get crd clusters.postgresql.cnpg.io >/dev/null 2>&1; then
  echo "CloudNativePG already installed (clusters.postgresql.cnpg.io CRD present)."
  exit 0
fi

echo "Installing CloudNativePG operator ${CNPG_VERSION}..."
kubectl apply --server-side -f "${MANIFEST_URL}"

echo "Waiting for CNPG controller..."
kubectl wait --for=condition=Available deployment/cnpg-controller-manager \
  -n cnpg-system --timeout=300s 2>/dev/null || \
  kubectl wait --for=condition=Available deployment -l app.kubernetes.io/name=cloudnative-pg \
  -n cnpg-system --timeout=300s

echo "CloudNativePG operator is ready."
