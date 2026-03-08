package integration_test

import (
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/Scorcher/dns-to-route-resolver/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestThreeNodeCluster(t *testing.T) {
	// Get free ports for our test instances
	port1, err := testutil.GetFreePort()
	require.NoError(t, err)

	port2, err := testutil.GetFreePort()
	require.NoError(t, err)

	port3, err := testutil.GetFreePort()
	require.NoError(t, err)

	// Create three test instances
	instance1, err := testutil.NewTestInstance(port1)
	require.NoError(t, err)
	defer instance1.Stop()

	instance2, err := testutil.NewTestInstance(port2)
	require.NoError(t, err)
	defer instance2.Stop()

	instance3, err := testutil.NewTestInstance(port3)
	require.NoError(t, err)
	defer instance3.Stop()

	// Start the first instance (no peers initially)
	err = instance1.Start(nil)
	require.NoError(t, err)

	// Start the second instance, connecting to the first
	err = instance2.Start([]string{"127.0.0.1:" + strconv.Itoa(port1)})
	require.NoError(t, err)

	// Start the third instance, connecting to the first
	err = instance3.Start([]string{"127.0.0.1:" + strconv.Itoa(port1)})
	require.NoError(t, err)

	// Give them time to discover each other
	time.Sleep(3 * time.Second)

	// Add a log entry to instance1 that should trigger a route
	err = instance1.AddLogEntry("192.168.1.100", "example.com", "A")
	require.NoError(t, err)

	// Verify the route was added to instance1
	assert.Eventually(t, func() bool {
		routes := instance1.App.GetNetworkManager().GetKnownNetworks()
		for _, r := range routes {
			if r.IP.Equal(net.ParseIP("192.168.1.0")) {
				return true
			}
		}
		return false
	}, 5*time.Second, 100*time.Millisecond, "instance1 should have the route")

	// Verify the route was propagated to instance2
	assert.Eventually(t, func() bool {
		routes := instance2.App.GetNetworkManager().GetKnownNetworks()
		for _, r := range routes {
			if r.IP.Equal(net.ParseIP("192.168.1.0")) {
				return true
			}
		}
		return false
	}, 5*time.Second, 100*time.Millisecond, "instance2 should have received the route")

	// Verify the route was propagated to instance3
	assert.Eventually(t, func() bool {
		routes := instance3.App.GetNetworkManager().GetKnownNetworks()
		for _, r := range routes {
			if r.IP.Equal(net.ParseIP("192.168.1.0")) {
				return true
			}
		}
		return false
	}, 5*time.Second, 100*time.Millisecond, "instance3 should have received the route")

	// Add a different route to instance3
	err = instance3.AddLogEntry("10.0.0.100", "example.com", "A")
	require.NoError(t, err)

	// Verify the new route was added to instance3
	assert.Eventually(t, func() bool {
		routes := instance3.App.GetNetworkManager().GetKnownNetworks()
		for _, r := range routes {
			if r.IP.Equal(net.ParseIP("10.0.0.0")) {
				return true
			}
		}
		return false
	}, 5*time.Second, 100*time.Millisecond, "instance3 should have the new route")

	// Verify the new route was propagated to instance1 and instance2
	for _, inst := range []*testutil.TestInstance{instance1, instance2} {
		assert.Eventually(t, func() bool {
			routes := inst.App.GetNetworkManager().GetKnownNetworks()
			for _, r := range routes {
				if r.IP.Equal(net.ParseIP("10.0.0.0")) {
					return true
				}
			}
			return false
		}, 5*time.Second, 100*time.Millisecond, "instance should have received the new route")
	}

	// Test network partition - stop instance2
	instance2.Stop()

	// Add a new route to instance1
	err = instance1.AddLogEntry("172.16.1.100", "example.com", "A")
	require.NoError(t, err)

	// Verify instance3 gets the new route
	assert.Eventually(t, func() bool {
		routes := instance3.App.GetNetworkManager().GetKnownNetworks()
		for _, r := range routes {
			if r.IP.Equal(net.ParseIP("172.16.1.0")) {
				return true
			}
		}
		return false
	}, 5*time.Second, 100*time.Millisecond, "instance3 should have received the route after partition")

	// Restart instance2
	instance2, err = testutil.NewTestInstance(port2)
	require.NoError(t, err)
	defer instance2.Stop()

	// Start it with connection to instance3
	err = instance2.Start([]string{"127.0.0.1:" + strconv.Itoa(port3)})
	require.NoError(t, err)

	// Verify instance2 eventually gets all routes
	assert.Eventually(t, func() bool {
		routes := instance2.App.GetNetworkManager().GetKnownNetworks()
		hasRoutes := make(map[string]bool)
		for _, r := range routes {
			hasRoutes[r.IP.String()] = true
		}
		return hasRoutes["192.168.1.0"] && hasRoutes["10.0.0.0"] && hasRoutes["172.16.1.0"]
	}, 10*time.Second, 100*time.Millisecond, "instance2 should have all routes after rejoining")
}
