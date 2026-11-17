// Package testutil supplies shared fixtures for isolated unit and integration tests.
package testutil

import "fmt"

// kubeconfigTemplate defines an inert local context with no credential helpers.
// Its unreachable endpoint makes accidental network use fail immediately.
var kubeconfigTemplate = `apiVersion: v1
kind: Config
current-context: test
clusters:
- name: cluster
  cluster:
    server: https://127.0.0.1:1
users:
- name: user
  user: {}
contexts:
- name: test
  context:
    cluster: cluster
    user: user
    namespace: %s
`

// Kubeconfig returns a minimal single-context fixture with the requested namespace.
func Kubeconfig(namespace string) []byte {
	return []byte(fmt.Sprintf(kubeconfigTemplate, namespace))
}
