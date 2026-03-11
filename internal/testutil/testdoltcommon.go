package testutil

import (
	"fmt"
	"net"
	"time"
)

// DoltDockerImage is the Docker image used for Dolt test containers.
//
// IMPORTANT: This version is PINNED to prevent silent test breakage from
// Dolt SQL dialect changes or query plan regressions. Before upgrading:
//
// 1. Review the Dolt changelog between current and target versions
// 2. Run the full test suite (including integration tests)
// 3. Pay special attention to search/query tests
// 4. Document any SQL compatibility issues in the upgrade commit
//
// See scripts/check-dolt-version.sh for automated upgrade validation.
const DoltDockerImage = "dolthub/dolt-sql-server:1.83.0"

// FindFreePort finds an available TCP port by binding to :0.
func FindFreePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port, nil
}

// WaitForServer polls until the server accepts TCP connections on the given port.
func WaitForServer(port int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	for time.Now().Before(deadline) {
		// #nosec G704 -- addr is always loopback (127.0.0.1) with a test-selected local port.
		conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
}
