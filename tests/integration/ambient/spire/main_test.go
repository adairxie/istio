//go:build integ
// +build integ

// Copyright Istio Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package spire

import (
	"testing"

	"istio.io/istio/pkg/config/constants"
	"istio.io/istio/pkg/config/protocol"
	"istio.io/istio/pkg/test/framework"
	"istio.io/istio/pkg/test/framework/components/crd"
	"istio.io/istio/pkg/test/framework/components/echo"
	"istio.io/istio/pkg/test/framework/components/echo/deployment"
	"istio.io/istio/pkg/test/framework/components/istio"
	"istio.io/istio/pkg/test/framework/components/istioctl"
	"istio.io/istio/pkg/test/framework/components/namespace"
	"istio.io/istio/pkg/test/framework/label"
	"istio.io/istio/pkg/test/framework/resource"
	"istio.io/istio/tests/integration/security/util/cert"
	"istio.io/istio/tests/util/sanitycheck"
)

var i istio.Instance

type EchoDeployments struct {
	// Namespace echo apps will be deployed
	Namespace namespace.Instance
	// Captured echo service
	Captured echo.Instances
	// Uncaptured echo Service
	Uncaptured echo.Instances

	// All echo services
	All echo.Instances
}

// TODO best way to get the systemnamespace before we actually install Istio? this is weirdly non-obvious
// TODO BML drop the bleggett image overrides when the next SPIRE release hits
var spireOverrides = `
global:
  spire:
    trustDomain: cluster.local
spire-agent:
    authorizedDelegates:
        - "spiffe://cluster.local/ns/istio-system/sa/ztunnel"
    sockets:
        admin:
            enabled: true
            mountOnHost: true
        hostBasePath: /run/spire/agent/sockets
    image:
        registry: gcr.io
        repository: solo-oss/bleggett/spire-agent
        pullPolicy: IfNotPresent
        tag: 08-02-2024-bbranch

spire-server:
    image:
        registry: gcr.io
        repository: solo-oss/bleggett/spire-server
        pullPolicy: IfNotPresent
        tag: 08-02-2024-bbranch
    persistence:
        type: emptyDir
`

// TestMain defines the entrypoint for pilot tests using a standard Istio installation.
// If a test requires a custom install it should go into its own package, otherwise it should go
// here to reuse a single install across tests.
func TestMain(m *testing.M) {
	// nolint: staticcheck
	framework.
		NewSuite(m).
		RequireSingleCluster().
		RequireMinVersion(26).
		Label(label.IPv4). // https://github.com/istio/istio/issues/41008
		Setup(func(t resource.Context) error {
			t.Settings().Ambient = true
			return nil
		}).
		Setup(func(t resource.Context) error {
			// *not* running this in Setup would be preferred, but it needs to
			// be installed *before* Istio is, or not all istio pods will go
			// healthy, and the test rig doesn't provide a nice way to install things
			// that Istio depends on before Istio
			err := DeploySpireWithOverrides(t, spireOverrides)
			if err != nil {
				if t.Settings().CIMode {
					namespace.Dump(t, SpireNamespace)
				}
			}
			return err
		}).
		Setup(istio.Setup(&i, func(ctx resource.Context, cfg *istio.Config) {
			// can't deploy VMs without eastwest gateway
			ctx.Settings().SkipVMs()
			cfg.EnableCNI = true
			cfg.DeployEastWestGW = false
			cfg.ControlPlaneValues = `
values:
  gateways:
    spire:
      workloads: true
  cni:
    repair:
      enabled: true
  ztunnel:
    spire:
      enabled: true
    terminationGracePeriodSeconds: 5
    env:
      SECRET_TTL: 5m
`
		}, cert.CreateCASecretAlt)).
		Teardown(func(t resource.Context) {
			TeardownSpire(t)
		}).
		Run()
}

const (
	Captured   = "captured"
	Uncaptured = "uncaptured"
)

func TestTrafficWithSpire(t *testing.T) {
	framework.NewTest(t).
		Run(func(t framework.TestContext) {
			ns, client, server := setupSmallTrafficTest(t, t)
			sanitycheck.RunTrafficTestClientServer(t, client, server)

			// Deploy waypoint
			crd.DeployGatewayAPIOrSkip(t)
			istioctl.NewOrFail(t, t, istioctl.Config{}).InvokeOrFail(t, []string{
				"waypoint",
				"apply",
				"--namespace",
				ns.Name(),
				"--enroll-namespace",
				"--wait",
			})
			CheckWaypointIsReady(t, ns.Name(), "waypoint")
			// Test again
			sanitycheck.RunTrafficTestClientServer(t, client, server)
		})
}

func setupSmallTrafficTest(t framework.TestContext, ctx resource.Context) (namespace.Instance, echo.Instance, echo.Instance) {
	var client, server echo.Instance
	testNs := namespace.NewOrFail(t, ctx, namespace.Config{
		Prefix: "default",
		Inject: false,
		Labels: map[string]string{
			constants.DataplaneModeLabel: "ambient",
			"istio-injection":            "disabled",
		},
	})
	deployment.New(ctx).
		With(&client, echo.Config{
			Service:   "client",
			Namespace: testNs,
			Ports:     []echo.Port{},
		}).
		With(&server, echo.Config{
			Service:   "server",
			Namespace: testNs,
			Ports: []echo.Port{
				{
					Name:         "http",
					Protocol:     protocol.HTTP,
					WorkloadPort: 8090,
				},
			},
		}).
		BuildOrFail(t)

	return testNs, client, server
}
