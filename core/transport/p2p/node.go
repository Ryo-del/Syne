package p2p

import (
	corecrypto "Syne/core/crypto"
	coreprotocol "Syne/core/protocol"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	libp2p "github.com/libp2p/go-libp2p"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	record "github.com/libp2p/go-libp2p-record"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/peerstore"
	lpprotocol "github.com/libp2p/go-libp2p/core/protocol"
	mdns "github.com/libp2p/go-libp2p/p2p/discovery/mdns"
	"github.com/libp2p/go-libp2p/p2p/host/autorelay"
	"github.com/libp2p/go-libp2p/p2p/protocol/holepunch"
	ma "github.com/multiformats/go-multiaddr"
)

const (
	streamProtocol             = lpprotocol.ID("/syne/packet/2.0.0")
	mdnsServiceName            = "_syne._udp"
	peerRecordTTL              = 2 * time.Minute
	defaultWriteDeadline       = 5 * time.Second
	defaultReadLimit     int64 = 256 * 1024
)

type PacketHandler func(msg coreprotocol.Message, sender peer.AddrInfo)
type PeerHandler func(info peer.AddrInfo)

type Node struct {
	ctx    context.Context
	cancel context.CancelFunc

	host host.Host
	dht  *dht.IpfsDHT
	mdns mdns.Service

	packetHandler PacketHandler
	peerHandler   PeerHandler

	mu         sync.RWMutex
	knownPeers map[peer.ID]peer.AddrInfo
	invites    map[string]inviteRecord
	wg         sync.WaitGroup
}

type peerRecord struct {
	PeerID    string   `json:"peer_id"`
	Addrs     []string `json:"addrs"`
	UpdatedAt int64    `json:"updated_at"`
	ExpiresAt int64    `json:"expires_at"`
}

type inviteRecord struct {
	PeerID    string `json:"peer_id"`
	UpdatedAt int64  `json:"updated_at"`
	ExpiresAt int64  `json:"expires_at"`
}

type syneValidator struct{}

func NewNode(parent context.Context, identity *corecrypto.Identity, packetHandler PacketHandler, peerHandler PeerHandler) (*Node, error) {
	if identity == nil || identity.PrivateKey == nil {
		return nil, fmt.Errorf("identity is required")
	}

	ctx, cancel := context.WithCancel(parent)
	node := &Node{
		ctx:           ctx,
		cancel:        cancel,
		packetHandler: packetHandler,
		peerHandler:   peerHandler,
		knownPeers:    make(map[peer.ID]peer.AddrInfo),
		invites:       make(map[string]inviteRecord),
	}

	h, err := libp2p.New(
		libp2p.Identity(identity.PrivateKey),
		libp2p.ListenAddrStrings(
			"/ip4/0.0.0.0/tcp/0",
			"/ip4/0.0.0.0/udp/0/quic-v1",
			"/ip6/::/tcp/0",
			"/ip6/::/udp/0/quic-v1",
		),
		libp2p.NATPortMap(),
		libp2p.EnableRelay(),
		libp2p.EnableRelayService(),
		libp2p.EnableNATService(),
		libp2p.EnableHolePunching(holepunch.DirectDialTimeout(2*time.Second)),
		libp2p.EnableAutoRelayWithPeerSource(node.relayPeerSource, autorelay.WithNumRelays(2), autorelay.WithBootDelay(20*time.Second)),
	)
	if err != nil {
		cancel()
		return nil, err
	}
	node.host = h
	node.host.SetStreamHandler(streamProtocol, node.handleStream)
	v := record.NamespacedValidator{
		"pk":   record.PublicKeyValidator{}, // Обязателен для глобальной сети
		"ipns": syneValidator{},             // IPFS требует этот ключ, подставим наш валидатор
		"syne": syneValidator{},             // Твой кастомный валидатор
	}
	kad, err := dht.New(ctx, h,
		dht.Mode(dht.ModeServer),
		dht.Validator(v),
		dht.ProtocolPrefix("/syne"),
	)
	if err != nil {
		_ = h.Close()
		cancel()
		return nil, fmt.Errorf("dht init: %w", err)
	}
	node.dht = kad
	go func() {
		time.Sleep(time.Second * 2)
		if err := kad.Bootstrap(node.ctx); err != nil {
			// Просто логируем для инфо, НЕ закрываем узел
			fmt.Printf("DHT Bootstrap info: %v\n", err)
		}
	}()
	for _, info := range dht.GetDefaultBootstrapPeerAddrInfos() {
		node.rememberPeer(info)
		h.Peerstore().AddAddrs(info.ID, info.Addrs, peerstore.PermanentAddrTTL)
		go func(info peer.AddrInfo) {
			ctx, cancel := context.WithTimeout(node.ctx, 5*time.Second)
			defer cancel()
			_ = h.Connect(ctx, info)
		}(info)
	}

	service := mdns.NewMdnsService(h, mdnsServiceName, node)
	if err := service.Start(); err != nil {
		node.Close()
		return nil, err
	}
	node.mdns = service

	// DHT table is likely empty at startup before LAN peers are discovered.
	// We ignore the error here; refreshLoop will retry publishing later.
	_ = node.PublishSelf()

	node.wg.Add(1)
	go node.refreshLoop()
	return node, nil
}

func (n *Node) Close() error {
	n.cancel()
	if n.mdns != nil {
		_ = n.mdns.Close()
	}
	if n.dht != nil {
		_ = n.dht.Close()
	}
	if n.host != nil {
		_ = n.host.Close()
	}
	n.wg.Wait()
	return nil
}

func (n *Node) ID() string {
	if n.host == nil {
		return ""
	}
	return n.host.ID().String()
}

func (n *Node) Addrs() []string {
	if n.host == nil {
		return nil
	}
	info := peer.AddrInfo{ID: n.host.ID(), Addrs: n.host.Addrs()}
	addrs, err := peer.AddrInfoToP2pAddrs(&info)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(addrs))
	for _, addr := range addrs {
		out = append(out, addr.String())
	}
	return out
}

func (n *Node) PublishSelf() error {
	record := peerRecord{
		PeerID:    n.ID(),
		Addrs:     n.Addrs(),
		UpdatedAt: time.Now().UnixMilli(),
		ExpiresAt: time.Now().Add(peerRecordTTL).UnixMilli(),
	}
	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(n.ctx, 8*time.Second)
	defer cancel()
	return n.dht.PutValue(ctx, peerDHTKey(record.PeerID), raw)
}

func (n *Node) PublishInvite(code string, ttl time.Duration) (inviteRecord, error) {
	code = strings.TrimSpace(code)
	if len(code) != 6 {
		return inviteRecord{}, fmt.Errorf("invite code must be 6 digits")
	}
	rec := inviteRecord{
		PeerID:    n.ID(),
		UpdatedAt: time.Now().UnixMilli(),
		ExpiresAt: time.Now().Add(ttl).UnixMilli(),
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		return inviteRecord{}, err
	}
	ctx, cancel := context.WithTimeout(n.ctx, 8*time.Second)
	defer cancel()
	if err := n.dht.PutValue(ctx, inviteDHTKey(code), raw); err != nil {
		return inviteRecord{}, err
	}
	n.mu.Lock()
	n.invites[code] = rec
	n.mu.Unlock()
	return rec, nil
}

func (n *Node) ResolveInvite(ctx context.Context, code string) (string, error) {
	code = strings.TrimSpace(code)
	raw, err := n.dht.GetValue(ctx, inviteDHTKey(code))
	if err != nil {
		return "", err
	}
	var rec inviteRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return "", err
	}
	if rec.ExpiresAt > 0 && rec.ExpiresAt < time.Now().UnixMilli() {
		return "", fmt.Errorf("invite code expired")
	}
	return rec.PeerID, nil
}

func (n *Node) ResolvePeer(ctx context.Context, peerID string) (peer.AddrInfo, error) {
	pid, err := peer.Decode(strings.TrimSpace(peerID))
	if err != nil {
		return peer.AddrInfo{}, err
	}

	n.mu.RLock()
	if info, ok := n.knownPeers[pid]; ok && len(info.Addrs) > 0 {
		n.mu.RUnlock()
		return info, nil
	}
	n.mu.RUnlock()

	if info := n.host.Peerstore().PeerInfo(pid); len(info.Addrs) > 0 {
		n.rememberPeer(info)
		return info, nil
	}

	if raw, err := n.dht.GetValue(ctx, peerDHTKey(peerID)); err == nil {
		if info, err := parsePeerRecord(raw); err == nil && len(info.Addrs) > 0 {
			n.rememberPeer(info)
			return info, nil
		}
	}

	info, err := n.dht.FindPeer(ctx, pid)
	if err == nil && len(info.Addrs) > 0 {
		n.rememberPeer(info)
		return info, nil
	}
	return peer.AddrInfo{}, fmt.Errorf("peer not found: %s", peerID)
}

func (n *Node) RememberHint(peerID, addr string) error {
	pid, err := peer.Decode(strings.TrimSpace(peerID))
	if err != nil {
		return err
	}
	maddr, err := tcpAddrToMultiaddr(strings.TrimSpace(addr), pid)
	if err != nil {
		return err
	}
	info, err := peer.AddrInfoFromP2pAddr(maddr)
	if err != nil {
		return err
	}
	n.rememberPeer(*info)
	return nil
}

func (n *Node) SendDirect(ctx context.Context, msg coreprotocol.Message, allowRelay bool) (coreprotocol.Strategy, error) {
	info, err := n.ResolvePeer(ctx, msg.TargetID)
	if err != nil {
		return coreprotocol.StrategyUnknown, err
	}
	filtered := filterAddrs(info.Addrs, allowRelay)
	if len(filtered) == 0 {
		if allowRelay {
			return coreprotocol.StrategyUnknown, fmt.Errorf("relay addresses unavailable")
		}
		return coreprotocol.StrategyUnknown, fmt.Errorf("direct addresses unavailable")
	}
	info.Addrs = filtered
	strategy := coreprotocol.StrategyDirect
	if hasRelayAddr(filtered) {
		strategy = coreprotocol.StrategyRelay
	}
	msg.Strategy = strategy
	if err := n.send(ctx, info, msg); err != nil {
		return coreprotocol.StrategyUnknown, err
	}
	return strategy, nil
}

func (n *Node) SendHop(ctx context.Context, msg coreprotocol.Message, excludePeerID string) error {
	exclude := strings.TrimSpace(excludePeerID)
	targets := n.KnownPeers()
	if len(targets) == 0 {
		return fmt.Errorf("no known peers for hop forwarding")
	}
	var sent int
	for _, info := range targets {
		if info.ID.String() == exclude || info.ID.String() == msg.From || info.ID.String() == msg.TargetID {
			continue
		}
		msg.Strategy = coreprotocol.StrategyHop
		if err := n.send(ctx, info, msg); err == nil {
			sent++
		}
	}
	if sent == 0 {
		return fmt.Errorf("no hop neighbors accepted the packet")
	}
	return nil
}

func (n *Node) KnownPeers() []peer.AddrInfo {
	n.mu.RLock()
	defer n.mu.RUnlock()
	items := make([]peer.AddrInfo, 0, len(n.knownPeers))
	for _, info := range n.knownPeers {
		if len(info.Addrs) == 0 {
			continue
		}
		items = append(items, info)
	}
	return items
}

func (n *Node) HandlePeerFound(info peer.AddrInfo) {
	n.rememberPeer(info)
}

func (n *Node) relayPeerSource(ctx context.Context, num int) <-chan peer.AddrInfo {
	out := make(chan peer.AddrInfo, num)
	go func() {
		defer close(out)
		seen := map[peer.ID]struct{}{}
		for _, info := range n.KnownPeers() {
			if len(seen) >= num {
				return
			}
			if info.ID == n.host.ID() || len(info.Addrs) == 0 {
				continue
			}
			seen[info.ID] = struct{}{}
			select {
			case out <- info:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}

func (n *Node) handleStream(stream network.Stream) {
	defer stream.Close()
	_ = stream.SetReadDeadline(time.Now().Add(defaultWriteDeadline))
	data, err := io.ReadAll(io.LimitReader(stream, defaultReadLimit))
	if err != nil {
		return
	}
	msg, err := coreprotocol.UnmarshalMessage(data)
	if err != nil {
		return
	}
	if err := coreprotocol.ValidateMessage(msg); err != nil {
		return
	}

	remote := peer.AddrInfo{
		ID:    stream.Conn().RemotePeer(),
		Addrs: []ma.Multiaddr{stream.Conn().RemoteMultiaddr()},
	}
	n.rememberPeer(remote)
	if n.packetHandler != nil {
		n.packetHandler(msg, remote)
	}
}

func (n *Node) send(ctx context.Context, info peer.AddrInfo, msg coreprotocol.Message) error {
	n.rememberPeer(info)
	wire, err := coreprotocol.MarshalMessage(msg)
	if err != nil {
		return err
	}
	timeoutCtx, cancel := context.WithTimeout(ctx, defaultWriteDeadline)
	defer cancel()
	n.host.Peerstore().AddAddrs(info.ID, info.Addrs, peerstore.TempAddrTTL)
	if err := n.host.Connect(timeoutCtx, info); err != nil {
		return err
	}
	stream, err := n.host.NewStream(timeoutCtx, info.ID, streamProtocol)
	if err != nil {
		return err
	}
	defer stream.Close()
	_ = stream.SetWriteDeadline(time.Now().Add(defaultWriteDeadline))
	_, err = stream.Write(wire)
	return err
}

func (n *Node) rememberPeer(info peer.AddrInfo) {
	if info.ID == "" {
		return
	}
	if len(info.Addrs) > 0 {
		n.host.Peerstore().AddAddrs(info.ID, info.Addrs, peerstore.PermanentAddrTTL)
	}
	n.mu.Lock()
	current := n.knownPeers[info.ID]
	if len(info.Addrs) == 0 {
		info.Addrs = current.Addrs
	}
	n.knownPeers[info.ID] = info
	n.mu.Unlock()

	if n.peerHandler != nil && info.ID != n.host.ID() {
		n.peerHandler(info)
	}
}

func (n *Node) refreshLoop() {
	defer n.wg.Done()
	ticker := time.NewTicker(45 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-n.ctx.Done():
			return
		case <-ticker.C:
			_ = n.PublishSelf()
		}
	}
}

func filterAddrs(addrs []ma.Multiaddr, allowRelay bool) []ma.Multiaddr {
	out := make([]ma.Multiaddr, 0, len(addrs))
	for _, addr := range addrs {
		hasRelay := strings.Contains(addr.String(), "/p2p-circuit")
		if allowRelay != hasRelay {
			if allowRelay || hasRelay {
				continue
			}
		}
		if !allowRelay && hasRelay {
			continue
		}
		if allowRelay && len(out) == 0 && hasRelay {
			out = append(out, addr)
			continue
		}
		if allowRelay && !hasRelay {
			continue
		}
		out = append(out, addr)
	}
	if allowRelay && len(out) == 0 {
		for _, addr := range addrs {
			if strings.Contains(addr.String(), "/p2p-circuit") {
				out = append(out, addr)
			}
		}
	}
	if !allowRelay && len(out) == 0 {
		for _, addr := range addrs {
			if !strings.Contains(addr.String(), "/p2p-circuit") {
				out = append(out, addr)
			}
		}
	}
	return out
}

func hasRelayAddr(addrs []ma.Multiaddr) bool {
	for _, addr := range addrs {
		if strings.Contains(addr.String(), "/p2p-circuit") {
			return true
		}
	}
	return false
}

func parsePeerRecord(raw []byte) (peer.AddrInfo, error) {
	var record peerRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return peer.AddrInfo{}, err
	}
	if record.ExpiresAt > 0 && record.ExpiresAt < time.Now().UnixMilli() {
		return peer.AddrInfo{}, fmt.Errorf("peer record expired")
	}
	pid, err := peer.Decode(record.PeerID)
	if err != nil {
		return peer.AddrInfo{}, err
	}
	info := peer.AddrInfo{ID: pid}
	for _, addr := range record.Addrs {
		maAddr, err := ma.NewMultiaddr(addr)
		if err != nil {
			continue
		}
		info.Addrs = append(info.Addrs, maAddr)
	}
	return info, nil
}

func peerDHTKey(peerID string) string {
	return "/syne/peer/" + strings.TrimSpace(peerID)
}

func inviteDHTKey(code string) string {
	return "/syne/rendezvous/" + strings.TrimSpace(code)
}

func tcpAddrToMultiaddr(addr string, pid peer.ID) (ma.Multiaddr, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	num, err := strconv.Atoi(port)
	if err != nil {
		return nil, err
	}
	prefix := "ip4"
	if parsed := net.ParseIP(host); parsed != nil && parsed.To4() == nil {
		prefix = "ip6"
	}
	return ma.NewMultiaddr(fmt.Sprintf("/%s/%s/tcp/%d/p2p/%s", prefix, host, num, pid.String()))
}

func (syneValidator) Validate(key string, value []byte) error {
	if !strings.HasPrefix(key, "/syne/") {
		return fmt.Errorf("unsupported namespace")
	}
	if len(value) == 0 {
		return fmt.Errorf("empty record")
	}
	var generic struct {
		UpdatedAt int64 `json:"updated_at"`
		ExpiresAt int64 `json:"expires_at"`
	}
	if err := json.Unmarshal(value, &generic); err != nil {
		return err
	}
	if generic.ExpiresAt > 0 && generic.ExpiresAt < time.Now().UnixMilli() {
		return fmt.Errorf("record expired")
	}
	return nil
}

func (syneValidator) Select(_ string, values [][]byte) (int, error) {
	best := 0
	var bestUpdated int64
	for i, raw := range values {
		var generic struct {
			UpdatedAt int64 `json:"updated_at"`
		}
		if err := json.Unmarshal(raw, &generic); err != nil {
			continue
		}
		if i == 0 || generic.UpdatedAt > bestUpdated {
			best = i
			bestUpdated = generic.UpdatedAt
		}
	}
	return best, nil
}

var _ mdns.Notifee = (*Node)(nil)
var _ record.Validator = syneValidator{}
