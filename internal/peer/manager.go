package peer

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"github.com/Scorcher/dns-to-route-resolver/internal/config"
	"github.com/Scorcher/dns-to-route-resolver/internal/log"
	"github.com/Scorcher/dns-to-route-resolver/internal/network"
	"github.com/hashicorp/memberlist"
)

// MessageType represents the type of message being sent between peers
type MessageType string

const (
	// MessageTypeSync is used for synchronizing routes between peers
	MessageTypeSync MessageType = "sync"
	// MessageTypeAnnounce is used to announce new routes
	MessageTypeAnnounce MessageType = "announce"
)

// Message represents a message sent between peers
type Message struct {
	Type      MessageType `json:"type"`
	Routes    []string    `json:"routes,omitempty"`
	Timestamp time.Time   `json:"timestamp"`
	From      string      `json:"from"`
}

// PeerManager manages peer-to-peer communication
type PeerManager struct {
	cfg          *config.Config
	logger       *log.Logger
	netMgr       *network.NetworkManager
	memberlist   *memberlist.Memberlist
	eventChan    chan *Message
	shutdownChan chan struct{}
	wg           sync.WaitGroup
}

// NewManager creates a new PeerManager instance
func NewManager(cfg *config.Config, netMgr *network.NetworkManager) (*PeerManager, error) {
	// Create a default config for memberlist
	memberlistConfig := memberlist.DefaultLocalConfig()
	memberlistConfig.Name = generateNodeName()
	memberlistConfig.BindPort = cfg.Network.PeerPort
	memberlistConfig.AdvertisePort = cfg.Network.PeerPort

	// Create a channel for events
	eventChan := make(chan *Message, 1000)

	return &PeerManager{
		cfg:          cfg,
		logger:       log.GetLogger(),
		netMgr:       netMgr,
		eventChan:    eventChan,
		shutdownChan: make(chan struct{}),
	}, nil
}

// Start starts the peer manager
func (p *PeerManager) Start() error {
	// Initialize memberlist
	memberlistConfig := memberlist.DefaultLocalConfig()
	memberlistConfig.Name = generateNodeName()
	memberlistConfig.BindPort = p.cfg.Network.PeerPort
	memberlistConfig.AdvertisePort = p.cfg.Network.PeerPort

	// Set up the delegate for custom message handling
	memberlistConfig.Delegate = &delegate{pm: p}

	// Create memberlist
	ml, err := memberlist.Create(memberlistConfig)
	if err != nil {
		return fmt.Errorf("failed to create memberlist: %w", err)
	}

	p.memberlist = ml

	// Join existing cluster if configured
	if len(p.cfg.Network.Peers) > 0 {
		n, err := ml.Join(p.cfg.Network.Peers)
		if err != nil {
			return fmt.Errorf("failed to join cluster: %w", err)
		}
		p.logger.Info(fmt.Sprintf("Joined cluster with %d nodes", n))
	} else {
		p.logger.Info("No peers configured, running in standalone mode")
	}

	// Start background tasks
	p.wg.Add(2)
	go p.syncRoutine()
	go p.processEvents()

	return nil
}

// Stop stops the peer manager
func (p *PeerManager) Stop() {
	close(p.shutdownChan)
	if p.memberlist != nil {
		_ = p.memberlist.Leave(5 * time.Second)
		_ = p.memberlist.Shutdown()
	}
	p.wg.Wait()
}

// AnnounceRoute announces a new route to all peers
func (p *PeerManager) AnnounceRoute(nw *net.IPNet) error {
	msg := &Message{
		Type:      MessageTypeAnnounce,
		Routes:    []string{nw.String()},
		Timestamp: time.Now(),
		From:      p.memberlist.LocalNode().Name,
	}

	return p.broadcast(msg)
}

// broadcast sends a message to all peers
func (p *PeerManager) broadcast(msg *Message) error {
	if p.memberlist == nil {
		return nil
	}

	// Encode the message
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	// Get all members except self
	members := p.memberlist.Members()
	for _, member := range members {
		if member.Name == p.memberlist.LocalNode().Name {
			continue // Skip self
		}

		// Send the message
		err := p.memberlist.SendReliable(member, data)
		if err != nil {
			p.logger.Error(fmt.Sprintf("Failed to send message to %s: %v", member.Name, err))
		}
	}

	return nil
}

// syncRoutine periodically synchronizes routes with peers
func (p *PeerManager) syncRoutine() {
	defer p.wg.Done()

	ticker := time.NewTicker(time.Duration(p.cfg.Settings.PeerDiscoveryInterval) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			p.syncRoutes()
		case <-p.shutdownChan:
			return
		}
	}
}

// syncRoutes synchronizes routes with all peers
func (p *PeerManager) syncRoutes() {
	if p.memberlist == nil || len(p.memberlist.Members()) <= 1 {
		return // No peers to sync with
	}

	// Get all known routes
	routes := p.netMgr.GetKnownNetworks()
	if len(routes) == 0 {
		return // Nothing to sync
	}

	// Convert to string slice
	routeStrs := make([]string, 0, len(routes))
	for _, route := range routes {
		routeStrs = append(routeStrs, route.String())
	}

	// Create sync message
	msg := &Message{
		Type:      MessageTypeSync,
		Routes:    routeStrs,
		Timestamp: time.Now(),
		From:      p.memberlist.LocalNode().Name,
	}

	// Broadcast the sync message
	_ = p.broadcast(msg)
}

// processEvents processes incoming events from peers
func (p *PeerManager) processEvents() {
	defer p.wg.Done()

	for {
		select {
		case msg := <-p.eventChan:
			p.handleMessage(msg)
		case <-p.shutdownChan:
			return
		}
	}
}

// handleMessage processes an incoming message
func (p *PeerManager) handleMessage(msg *Message) {
	switch msg.Type {
	case MessageTypeSync:
		p.handleSyncMessage(msg)
	case MessageTypeAnnounce:
		p.handleAnnounceMessage(msg)
	}
}

// handleSyncMessage handles a sync message from a peer
func (p *PeerManager) handleSyncMessage(msg *Message) {
	for _, routeStr := range msg.Routes {
		_, nw, err := net.ParseCIDR(routeStr)
		if err != nil {
			p.logger.Error(fmt.Sprintf("Failed to parse route %s: %v", routeStr, err))
			continue
		}

		// Add the route if we don't have it
		if err := p.netMgr.AddNetwork(nw.IP); err != nil {
			p.logger.Error(fmt.Sprintf("Failed to add route %s: %v", routeStr, err))
		}
	}
}

// handleAnnounceMessage handles an announce message from a peer
func (p *PeerManager) handleAnnounceMessage(msg *Message) {
	// For now, we'll handle it the same way as a sync message
	// In the future, we might want to handle announcements differently
	p.handleSyncMessage(msg)
}

// delegate implements memberlist.Delegate for custom message handling
type delegate struct {
	pm *PeerManager
}

// NodeMeta is used to retrieve meta-data about the current node
func (d *delegate) NodeMeta(limit int) []byte {
	return []byte{} // No metadata for now
}

// NotifyMsg is called when a message is received from a peer
func (d *delegate) NotifyMsg(b []byte) {
	var msg Message
	if err := json.Unmarshal(b, &msg); err != nil {
		d.pm.logger.Error(fmt.Sprintf("Failed to unmarshal message: %v", err))
		return
	}

	// Send the message to the event channel
	select {
	case d.pm.eventChan <- &msg:
	default:
		d.pm.logger.Warn("Event channel full, dropping message")
	}
}

// GetBroadcasts is called when user messages can be broadcasted
func (d *delegate) GetBroadcasts(overhead, limit int) [][]byte {
	// We don't use the built-in broadcast mechanism
	return nil
}

// LocalState is used for a TCP Push/Pull
func (d *delegate) LocalState(join bool) []byte {
	// Get all known routes
	routes := d.pm.netMgr.GetKnownNetworks()
	routeStrs := make([]string, 0, len(routes))
	for _, route := range routes {
		routeStrs = append(routeStrs, route.String())
	}

	// Create a message with all routes
	msg := Message{
		Type:      MessageTypeSync,
		Routes:    routeStrs,
		Timestamp: time.Now(),
		From:      d.pm.memberlist.LocalNode().Name,
	}

	// Marshal the message
	data, err := json.Marshal(msg)
	if err != nil {
		d.pm.logger.Error(fmt.Sprintf("Failed to marshal local state: %v", err))
		return nil
	}

	return data
}

// MergeRemoteState is called after a TCP Push/Pull
func (d *delegate) MergeRemoteState(buf []byte, join bool) {
	var msg Message
	if err := json.Unmarshal(buf, &msg); err != nil {
		d.pm.logger.Error(fmt.Sprintf("Failed to unmarshal remote state: %v", err))
		return
	}

	d.pm.handleMessage(&msg)
}

// generateNodeName generates a unique name for this node
func generateNodeName() string {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}
	return fmt.Sprintf("%s-%d", hostname, time.Now().UnixNano())
}
