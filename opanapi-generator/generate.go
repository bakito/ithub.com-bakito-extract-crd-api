//go:build generate
// +build generate

// run the generator
//go:generate go run . -target . -crd certificates.cert-manager.io.yaml -crd certificaterequests.cert-manager.io.yaml -crd clusterissuers.cert-manager.io.yaml

// Generate deepcopy methodsets and CRD manifests
//go:generate go run -tags generate sigs.k8s.io/controller-tools/cmd/controller-gen object paths=./v1

package main

import (
	_ "sigs.k8s.io/controller-tools/cmd/controller-gen" //nolint:typecheck
)
