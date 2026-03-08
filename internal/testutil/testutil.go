package testutil

import (
	"io/ioutil"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Scorcher/dns-to-route-resolver/internal/app"
	"github.com/Scorcher/dns-to-route-resolver/internal/config"
)

type TestInstance struct {
	Dir      string
	Config   *config.Config
	App      *app.App
	stopOnce sync.Once
}

func NewTestInstance(basePort int) (*TestInstance, error) {
	tempDir, err := ioutil.TempDir("", "dns-to-route-test-*")
	if err != nil {
		return nil, err
	}

	// Create test config
	cfg := &config.Config{
		Log: config.LogConfig{
			Level:  "debug",
			Format: "console",
		},
		DNSLog: config.DNSLogConfig{
			Path:   filepath.Join(tempDir, "query.log"),
			Follow: true,
		},
		Network: config.NetworkConfig{
			MonitoredDomains: []string{"example.com"},
			PeerPort:         basePort,
		},
		Metrics: config.MetricsConfig{
			Enabled: true,
			Port:    basePort + 1,
			Path:    "/metrics",
		},
		Persistence: config.PersistenceConfig{
			StateFile: filepath.Join(tempDir, "state.json"),
		},
	}

	// Create empty log file
	f, err := os.Create(cfg.DNSLog.Path)
	if err != nil {
		os.RemoveAll(tempDir)
		return nil, err
	}
	f.Close()

	// Create app
	app, err := app.NewApp(cfg)
	if err != nil {
		os.RemoveAll(tempDir)
		return nil, err
	}

	return &TestInstance{
		Dir:    tempDir,
		Config: cfg,
		App:    app,
	}, nil
}

func (ti *TestInstance) Start(peers []string) error {
	ti.Config.Network.Peers = peers
	go func() {
		_ = ti.App.Start()
	}()

	// Wait for the app to be ready
	time.Sleep(1 * time.Second)
	return nil
}

func (ti *TestInstance) Stop() {
	ti.stopOnce.Do(func() {
		ti.App.Stop()
		os.RemoveAll(ti.Dir)
	})
}

func (ti *TestInstance) AddLogEntry(ip, domain, qtype string) error {
	f, err := os.OpenFile(ti.Config.DNSLog.Path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.WriteString("[" + time.Now().Format("2006-01-02 15:04:05") + "] " + ip + " " + domain + " " + qtype + "\n")
	return err
}

func GetFreePort() (int, error) {
	addr, err := net.ResolveTCPAddr("tcp", "localhost:0")
	if err != nil {
		return 0, err
	}

	l, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func WaitForCondition(timeout time.Duration, condition func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}
