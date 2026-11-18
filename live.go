package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	liveQueue         = 64
	liveWriteWait     = 10 * time.Second
	livePongWait      = 70 * time.Second
	livePingPeriod    = 25 * time.Second
	liveMaxMessage    = 4096
	seatLeaseTTL      = 30 * time.Second
	incenseArrivalTTL = 60 * time.Second
	incenseStartedTTL = 60 * time.Second
	offeringTTL       = 120 * time.Second
)

type livePeer struct {
	ID      string  `json:"id"`
	X       float64 `json:"x"`
	Z       float64 `json:"z"`
	RY      float64 `json:"ry"`
	Mode    string  `json:"mode"`
	Greet   bool    `json:"greet"`
	Bow     string  `json:"bow"`
	GreetAt int64   `json:"greetAt,omitempty"`
	BowAt   int64   `json:"bowAt,omitempty"`
}
type liveClient struct {
	conn               *websocket.Conn
	send               chan []byte
	room               string
	peer               livePeer
	seatRequests       map[int64]seatResult
	incenseRequests    map[int64]bool
	offerRequests      map[int64]bool
	seatRequestHigh    int64
	incenseRequestHigh int64
	offerRequestHigh   int64
	seatRequestSeen    bool
	incenseRequestSeen bool
	offerRequestSeen   bool
	done               chan struct{}
}

type seatLease struct {
	leaseID   string
	cushion   int
	owner     *liveClient
	visitorID string
	status    string
	expiresAt int64
}

type seatResult struct {
	requestID int64
	cushion   *int
	leaseID   string
}

type incenseStarted struct {
	ID string `json:"id"`
	At int64  `json:"at"`
}

type incenseOffering struct {
	ID        string `json:"id"`
	VisitorID string `json:"visitorId"`
	At        int64  `json:"at"`
	ExpiresAt int64  `json:"expiresAt"`
}

type incenseQueueState struct {
	revision  int64
	members   []string
	owner     *liveClient
	turnID    string
	expiresAt int64
	started   *incenseStarted
	offered   bool
}

type liveRoomState struct {
	seats     map[int]*seatLease
	queue     incenseQueueState
	offerings []incenseOffering
}

type LiveHub struct {
	mu        sync.RWMutex
	rooms     map[string]map[*liveClient]struct{}
	roomState map[string]*liveRoomState
	stop      chan struct{}
	stopOnce  sync.Once
}

func NewLiveHub() *LiveHub {
	h := &LiveHub{rooms: make(map[string]map[*liveClient]struct{}), roomState: make(map[string]*liveRoomState), stop: make(chan struct{})}
	go h.expiryLoop()
	return h
}

func (h *LiveHub) Close() { h.stopOnce.Do(func() { close(h.stop) }) }
func (h *LiveHub) Count(room string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.rooms[room])
}

func (h *LiveHub) stateLocked(room string) *liveRoomState {
	s := h.roomState[room]
	if s == nil {
		s = &liveRoomState{seats: make(map[int]*seatLease), queue: incenseQueueState{members: make([]string, 0)}, offerings: make([]incenseOffering, 0)}
		h.roomState[room] = s
	}
	if s.seats == nil {
		s.seats = make(map[int]*seatLease)
	}
	if s.queue.members == nil {
		s.queue.members = make([]string, 0)
	}
	if s.offerings == nil {
		s.offerings = make([]incenseOffering, 0)
	}
	return s
}

func (h *LiveHub) seatSnapshotLocked(room string) any {
	seats := make([]map[string]any, 0)
	for _, lease := range h.stateLocked(room).seats {
		seats = append(seats, map[string]any{"id": lease.visitorID, "cushion": lease.cushion, "status": lease.status})
	}
	sort.Slice(seats, func(i, j int) bool { return seats[i]["cushion"].(int) < seats[j]["cushion"].(int) })
	return map[string]any{"t": "seats", "seats": seats}
}

func (h *LiveHub) incenseSnapshotLocked(room string, now int64) any {
	q := &h.stateLocked(room).queue
	var activeID *string
	if q.owner != nil {
		id := q.owner.peer.ID
		activeID = &id
	}
	return map[string]any{
		"t": "incense-state", "serverTime": now, "turnId": q.turnID,
		"queue":     map[string]any{"revision": q.revision, "members": append([]string{}, q.members...), "activeId": activeID, "expiresAt": q.expiresAt},
		"offerings": append([]incenseOffering{}, h.stateLocked(room).offerings...), "started": q.started,
	}
}

func marshalLive(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}

func (h *LiveHub) broadcastSeatsLocked(room string) []*liveClient {
	return h.broadcastLocked(room, marshalLive(h.seatSnapshotLocked(room)), nil)
}

func (h *LiveHub) broadcastIncenseLocked(room string, now int64) []*liveClient {
	return h.broadcastLocked(room, marshalLive(h.incenseSnapshotLocked(room, now)), nil)
}

func (h *LiveHub) sendLocked(c *liveClient, msg any) bool {
	b := marshalLive(msg)
	return b != nil && h.enqueue(c, b)
}

func cacheLiveRequest[T any](cache map[int64]T, id int64, value T) {
	const maxCachedRequests = 64
	if _, exists := cache[id]; !exists && len(cache) >= maxCachedRequests {
		var oldest int64
		first := true
		for requestID := range cache {
			if first || requestID < oldest {
				oldest, first = requestID, false
			}
		}
		delete(cache, oldest)
	}
	cache[id] = value
}

func requestIsStale(id, high int64, seen bool) bool { return seen && id <= high }

func (h *LiveHub) hasClientLocked(room string, c *liveClient) bool {
	_, ok := h.rooms[room][c]
	return ok
}

func (h *LiveHub) removeQueueMemberLocked(room string, c *liveClient, now int64) bool {
	s := h.stateLocked(room)
	q := &s.queue
	idx := -1
	for i, id := range q.members {
		if id == c.peer.ID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return false
	}
	if q.owner == c {
		h.advanceQueueLocked(room, true, now)
		return true
	}
	q.members = append(q.members[:idx], q.members[idx+1:]...)
	q.revision++
	return true
}

func (h *LiveHub) advanceQueueLocked(room string, removeOwner bool, now int64) {
	s := h.stateLocked(room)
	q := &s.queue
	if removeOwner && q.owner != nil {
		id := q.owner.peer.ID
		for i, member := range q.members {
			if member == id {
				q.members = append(q.members[:i], q.members[i+1:]...)
				break
			}
		}
	}
	connected := make(map[string]*liveClient)
	for c := range h.rooms[room] {
		connected[c.peer.ID] = c
	}
	kept := q.members[:0]
	for _, id := range q.members {
		if connected[id] != nil {
			kept = append(kept, id)
		}
	}
	q.members = kept
	q.owner, q.turnID, q.started = nil, "", nil
	q.offered = false
	q.expiresAt = 0
	if len(q.members) > 0 {
		q.owner = connected[q.members[0]]
		q.turnID = newID()
		q.expiresAt = now + incenseArrivalTTL.Milliseconds()
	}
	q.revision++
}

func (h *LiveHub) expireRoomLocked(room string, now int64) (bool, bool) {
	s := h.stateLocked(room)
	seatsChanged, incenseChanged := false, false
	for cushion, lease := range s.seats {
		if lease.status == "reserved" && lease.expiresAt > 0 && now >= lease.expiresAt {
			delete(s.seats, cushion)
			seatsChanged = true
		}
	}
	for i := 0; i < len(s.offerings); {
		if now >= s.offerings[i].ExpiresAt {
			s.offerings = append(s.offerings[:i], s.offerings[i+1:]...)
			incenseChanged = true
		} else {
			i++
		}
	}
	q := &s.queue
	if q.owner != nil && q.expiresAt > 0 && now >= q.expiresAt {
		h.advanceQueueLocked(room, true, now)
		incenseChanged = true
	}
	return seatsChanged, incenseChanged
}

func (h *LiveHub) expiryLoop() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			now := time.Now().UnixMilli()
			h.mu.Lock()
			var slow []*liveClient
			for room := range h.roomState {
				seats, incense := h.expireRoomLocked(room, now)
				if seats {
					slow = append(slow, h.broadcastSeatsLocked(room)...)
				}
				if incense {
					slow = append(slow, h.broadcastIncenseLocked(room, now)...)
				}
				if len(h.rooms[room]) == 0 && len(h.stateLocked(room).seats) == 0 && len(h.stateLocked(room).queue.members) == 0 && len(h.stateLocked(room).offerings) == 0 {
					delete(h.roomState, room)
				}
			}
			h.mu.Unlock()
			for _, c := range slow {
				go c.conn.Close()
			}
		case <-h.stop:
			return
		}
	}
}
func (h *LiveHub) peersLocked(room string, except *liveClient) []livePeer {
	out := []livePeer{}
	for c := range h.rooms[room] {
		if c != except {
			out = append(out, c.peer)
		}
	}
	return out
}
func (h *LiveHub) enqueue(c *liveClient, b []byte) bool {
	select {
	case c.send <- b:
		return true
	default:
		return false
	}
}
func (h *LiveHub) broadcast(room string, msg any, except *liveClient) {
	b, e := json.Marshal(msg)
	if e != nil {
		return
	}
	h.mu.Lock()
	list := h.broadcastLocked(room, b, except)
	h.mu.Unlock()
	for _, c := range list {
		go c.conn.Close()
	}
}
func (h *LiveHub) broadcastLocked(room string, b []byte, except *liveClient) []*liveClient {
	var slow []*liveClient
	for c := range h.rooms[room] {
		if c != except && !h.enqueue(c, b) {
			slow = append(slow, c)
		}
	}
	return slow
}
func (h *LiveHub) remove(c *liveClient) {
	h.mu.Lock()
	set := h.rooms[c.room]
	if _, ok := set[c]; !ok {
		h.mu.Unlock()
		return
	}
	delete(set, c)
	if len(set) == 0 {
		delete(h.rooms, c.room)
	}
	count := len(set)
	data, _ := json.Marshal(map[string]any{"t": "leave", "id": c.peer.ID, "count": count})
	slow := h.broadcastLocked(c.room, data, nil)
	seatsChanged := false
	for cushion, lease := range h.stateLocked(c.room).seats {
		if lease.owner == c {
			delete(h.stateLocked(c.room).seats, cushion)
			seatsChanged = true
		}
	}
	incenseChanged := h.removeQueueMemberLocked(c.room, c, time.Now().UnixMilli())
	if seatsChanged {
		slow = append(slow, h.broadcastSeatsLocked(c.room)...)
	}
	if incenseChanged {
		slow = append(slow, h.broadcastIncenseLocked(c.room, time.Now().UnixMilli())...)
	}
	h.mu.Unlock()
	for _, peer := range slow {
		go peer.conn.Close()
	}
}

var liveUpgrader = websocket.Upgrader{ReadBufferSize: 1024, WriteBufferSize: 1024, CheckOrigin: func(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if strings.EqualFold(u.Host, r.Host) {
		return true
	}
	switch strings.ToLower(origin) {
	case "capacitor://localhost", "https://localhost", "http://localhost":
		return true
	}
	return false
}}

func (h *LiveHub) ServeHTTP(rooms *RoomRegistry, w http.ResponseWriter, r *http.Request) {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		http.Error(w, `{"ok":false,"error":"websocket required"}`, http.StatusUpgradeRequired)
		return
	}
	room := r.URL.Query().Get("room")
	if !rooms.Has(room) {
		room = "maru"
	}
	conn, err := liveUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	id := sanitizeID(r.URL.Query().Get("visitor"))
	h.mu.Lock()
	if id == "" || h.hasIDLocked(room, id) {
		id = newID()
	}
	c := &liveClient{conn: conn, room: room, peer: livePeer{ID: id, Bow: "idle", Mode: "walk"}, send: make(chan []byte, liveQueue), done: make(chan struct{})}
	c.seatRequests = make(map[int64]seatResult)
	c.incenseRequests = make(map[int64]bool)
	c.offerRequests = make(map[int64]bool)
	now := time.Now().UnixMilli()
	seatsExpired, incenseExpired := h.expireRoomLocked(room, now)
	var slow []*liveClient
	if seatsExpired {
		slow = append(slow, h.broadcastSeatsLocked(room)...)
	}
	if incenseExpired {
		slow = append(slow, h.broadcastIncenseLocked(room, now)...)
	}
	set := h.rooms[room]
	if set == nil {
		set = map[*liveClient]struct{}{}
		h.rooms[room] = set
	}
	if len(set) >= RoomCapacity {
		h.mu.Unlock()
		for _, peer := range slow {
			go peer.conn.Close()
		}
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseTryAgainLater, "room full"), time.Now().Add(liveWriteWait))
		_ = conn.Close()
		return
	}
	set[c] = struct{}{}
	peers := h.peersLocked(room, c)
	count := len(set)
	welcome, _ := json.Marshal(map[string]any{"t": "welcome", "id": id, "room": room, "count": count, "serverTime": time.Now().UnixMilli(), "peers": peers})
	queued := h.enqueue(c, welcome) && h.sendLocked(c, h.seatSnapshotLocked(room)) && h.sendLocked(c, h.incenseSnapshotLocked(room, now))
	join, _ := json.Marshal(map[string]any{"t": "join", "id": id, "count": count, "x": 0, "z": 0, "ry": 0, "mode": "walk"})
	slow = append(slow, h.broadcastLocked(room, join, c)...)
	h.mu.Unlock()
	for _, peer := range slow {
		go peer.conn.Close()
	}
	if !queued {
		conn.Close()
		h.remove(c)
		return
	}
	go c.writePump()
	c.readPump(h)
}
func (h *LiveHub) hasIDLocked(room, id string) bool {
	for c := range h.rooms[room] {
		if c.peer.ID == id {
			return true
		}
	}
	return false
}
func (c *liveClient) writePump() {
	defer c.conn.Close()
	ticker := time.NewTicker(livePingPeriod)
	defer ticker.Stop()
	for {
		select {
		case b, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(liveWriteWait))
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if c.conn.WriteMessage(websocket.TextMessage, b) != nil {
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(liveWriteWait))
			if c.conn.WriteMessage(websocket.PingMessage, nil) != nil {
				return
			}
		case <-c.done:
			return
		}
	}
}
func (c *liveClient) readPump(h *LiveHub) {
	defer func() {
		h.remove(c)
		_ = c.conn.Close()
		if c.done != nil {
			close(c.done)
		}
	}()
	c.conn.SetReadLimit(liveMaxMessage)
	_ = c.conn.SetReadDeadline(time.Now().Add(livePongWait))
	c.conn.SetPongHandler(func(string) error { return c.conn.SetReadDeadline(time.Now().Add(livePongWait)) })
	for {
		_, b, e := c.conn.ReadMessage()
		if e != nil {
			return
		}
		c.handle(h, b)
	}
}

func closeSlowPeers(peers []*liveClient) {
	for _, c := range peers {
		go c.conn.Close()
	}
}

func (h *LiveHub) expireAndBroadcastLocked(room string, now int64) []*liveClient {
	seats, incense := h.expireRoomLocked(room, now)
	var slow []*liveClient
	if seats {
		slow = append(slow, h.broadcastSeatsLocked(room)...)
	}
	if incense {
		slow = append(slow, h.broadcastIncenseLocked(room, now)...)
	}
	return slow
}

func (h *LiveHub) releaseSeatsLocked(room string, owner *liveClient) bool {
	changed := false
	for cushion, lease := range h.stateLocked(room).seats {
		if lease.owner == owner {
			delete(h.stateLocked(room).seats, cushion)
			changed = true
		}
	}
	return changed
}

func (h *LiveHub) handleSeatRequest(c *liveClient, requestID int64, candidates *[]int) {
	now := time.Now().UnixMilli()
	h.mu.Lock()
	slow := h.expireAndBroadcastLocked(c.room, now)
	if !h.hasClientLocked(c.room, c) {
		h.mu.Unlock()
		closeSlowPeers(slow)
		return
	}
	if c.seatRequests == nil {
		c.seatRequests = make(map[int64]seatResult)
	}
	if cached, ok := c.seatRequests[requestID]; ok {
		if !h.sendLocked(c, map[string]any{"t": "seat-result", "requestId": cached.requestID, "cushion": cached.cushion, "leaseId": cached.leaseID}) {
			slow = append(slow, c)
		}
		h.mu.Unlock()
		closeSlowPeers(slow)
		return
	}
	result := seatResult{requestID: requestID, leaseID: ""}
	if requestIsStale(requestID, c.seatRequestHigh, c.seatRequestSeen) {
		cacheLiveRequest(c.seatRequests, requestID, result)
		if !h.sendLocked(c, map[string]any{"t": "seat-result", "requestId": requestID, "cushion": nil, "leaseId": ""}) {
			slow = append(slow, c)
		}
		h.mu.Unlock()
		closeSlowPeers(slow)
		return
	}
	if !c.seatRequestSeen || requestID > c.seatRequestHigh {
		c.seatRequestHigh, c.seatRequestSeen = requestID, true
	}
	s := h.stateLocked(c.room)
	for _, lease := range s.seats {
		if lease.owner == c {
			cushion := lease.cushion
			result.cushion, result.leaseID = &cushion, lease.leaseID
			break
		}
	}
	seatsChanged := false
	if result.cushion == nil && candidates != nil {
		seen := make(map[int]struct{})
		for _, cushion := range *candidates {
			if cushion < 0 || cushion >= 100 {
				continue
			}
			if _, ok := seen[cushion]; ok {
				continue
			}
			seen[cushion] = struct{}{}
			if _, occupied := s.seats[cushion]; occupied {
				continue
			}
			chosen := cushion
			lease := &seatLease{leaseID: newID(), cushion: cushion, owner: c, visitorID: c.peer.ID, status: "reserved", expiresAt: now + seatLeaseTTL.Milliseconds()}
			s.seats[cushion] = lease
			result.cushion, result.leaseID = &chosen, lease.leaseID
			seatsChanged = true
			break
		}
	}
	cacheLiveRequest(c.seatRequests, requestID, result)
	if !h.sendLocked(c, map[string]any{"t": "seat-result", "requestId": result.requestID, "cushion": result.cushion, "leaseId": result.leaseID}) {
		slow = append(slow, c)
	}
	if seatsChanged {
		slow = append(slow, h.broadcastSeatsLocked(c.room)...)
	}
	h.mu.Unlock()
	closeSlowPeers(slow)
}

func (h *LiveHub) handleSeatArrived(c *liveClient, leaseID string) {
	now := time.Now().UnixMilli()
	h.mu.Lock()
	slow := h.expireAndBroadcastLocked(c.room, now)
	changed := false
	for _, lease := range h.stateLocked(c.room).seats {
		if lease.owner == c && lease.leaseID == leaseID && lease.status == "reserved" {
			lease.status, lease.expiresAt = "occupied", 0
			changed = true
			break
		}
	}
	if changed {
		slow = append(slow, h.broadcastSeatsLocked(c.room)...)
	}
	h.mu.Unlock()
	closeSlowPeers(slow)
}

func (h *LiveHub) handleSeatRelease(c *liveClient, leaseID string) {
	now := time.Now().UnixMilli()
	h.mu.Lock()
	slow := h.expireAndBroadcastLocked(c.room, now)
	changed := false
	for cushion, lease := range h.stateLocked(c.room).seats {
		if lease.owner == c && lease.leaseID == leaseID {
			delete(h.stateLocked(c.room).seats, cushion)
			changed = true
			break
		}
	}
	if changed {
		slow = append(slow, h.broadcastSeatsLocked(c.room)...)
	}
	h.mu.Unlock()
	closeSlowPeers(slow)
}

func (h *LiveHub) handleIncenseJoin(c *liveClient, requestID int64) {
	now := time.Now().UnixMilli()
	h.mu.Lock()
	slow := h.expireAndBroadcastLocked(c.room, now)
	if !h.hasClientLocked(c.room, c) {
		h.mu.Unlock()
		closeSlowPeers(slow)
		return
	}
	if c.incenseRequests == nil {
		c.incenseRequests = make(map[int64]bool)
	}
	if cached, ok := c.incenseRequests[requestID]; ok {
		if !h.sendLocked(c, map[string]any{"t": "incense-result", "requestId": requestID, "ok": cached}) {
			slow = append(slow, c)
		}
		h.mu.Unlock()
		closeSlowPeers(slow)
		return
	}
	if requestIsStale(requestID, c.incenseRequestHigh, c.incenseRequestSeen) {
		cacheLiveRequest(c.incenseRequests, requestID, false)
		if !h.sendLocked(c, map[string]any{"t": "incense-result", "requestId": requestID, "ok": false}) {
			slow = append(slow, c)
		}
		h.mu.Unlock()
		closeSlowPeers(slow)
		return
	}
	if !c.incenseRequestSeen || requestID > c.incenseRequestHigh {
		c.incenseRequestHigh, c.incenseRequestSeen = requestID, true
	}
	s := h.stateLocked(c.room)
	joined := false
	for _, id := range s.queue.members {
		if id == c.peer.ID {
			joined = true
			break
		}
	}
	seatsChanged := h.releaseSeatsLocked(c.room, c)
	queueChanged := false
	if !joined {
		q := &s.queue
		q.members = append(q.members, c.peer.ID)
		q.revision++
		queueChanged = true
		if q.owner == nil {
			q.owner = c
			q.turnID = newID()
			q.expiresAt = now + incenseArrivalTTL.Milliseconds()
			q.started, q.offered = nil, false
		}
	}
	cacheLiveRequest(c.incenseRequests, requestID, true)
	if !h.sendLocked(c, map[string]any{"t": "incense-result", "requestId": requestID, "ok": true}) {
		slow = append(slow, c)
	}
	if seatsChanged {
		slow = append(slow, h.broadcastSeatsLocked(c.room)...)
	}
	if queueChanged {
		slow = append(slow, h.broadcastIncenseLocked(c.room, now)...)
	}
	h.mu.Unlock()
	closeSlowPeers(slow)
}

func (h *LiveHub) handleIncenseStart(c *liveClient, turnID string) {
	now := time.Now().UnixMilli()
	h.mu.Lock()
	slow := h.expireAndBroadcastLocked(c.room, now)
	q := &h.stateLocked(c.room).queue
	if h.hasClientLocked(c.room, c) && q.owner == c && turnID != "" && turnID == q.turnID && q.started == nil {
		q.started = &incenseStarted{ID: c.peer.ID, At: now}
		q.expiresAt = now + incenseStartedTTL.Milliseconds()
		q.revision++
		slow = append(slow, h.broadcastIncenseLocked(c.room, now)...)
	}
	h.mu.Unlock()
	closeSlowPeers(slow)
}

func (h *LiveHub) handleIncenseOffer(c *liveClient, requestID int64, turnID string) {
	now := time.Now().UnixMilli()
	h.mu.Lock()
	slow := h.expireAndBroadcastLocked(c.room, now)
	if !h.hasClientLocked(c.room, c) {
		h.mu.Unlock()
		closeSlowPeers(slow)
		return
	}
	if c.offerRequests == nil {
		c.offerRequests = make(map[int64]bool)
	}
	// Offers use a separate request cache so incense-join IDs cannot collide with them.
	if cached, ok := c.offerRequests[requestID]; ok {
		if !h.sendLocked(c, map[string]any{"t": "incense-result", "requestId": requestID, "ok": cached}) {
			slow = append(slow, c)
		}
		h.mu.Unlock()
		closeSlowPeers(slow)
		return
	}
	if requestIsStale(requestID, c.offerRequestHigh, c.offerRequestSeen) {
		cacheLiveRequest(c.offerRequests, requestID, false)
		if !h.sendLocked(c, map[string]any{"t": "incense-result", "requestId": requestID, "ok": false}) {
			slow = append(slow, c)
		}
		h.mu.Unlock()
		closeSlowPeers(slow)
		return
	}
	if !c.offerRequestSeen || requestID > c.offerRequestHigh {
		c.offerRequestHigh, c.offerRequestSeen = requestID, true
	}
	q := &h.stateLocked(c.room).queue
	ok := q.owner == c && turnID != "" && turnID == q.turnID && q.started != nil && !q.offered
	if ok {
		h.stateLocked(c.room).offerings = append(h.stateLocked(c.room).offerings, incenseOffering{ID: newID(), VisitorID: c.peer.ID, At: now, ExpiresAt: now + offeringTTL.Milliseconds()})
		q.offered = true
		slow = append(slow, h.broadcastIncenseLocked(c.room, now)...)
	}
	cacheLiveRequest(c.offerRequests, requestID, ok)
	if !h.sendLocked(c, map[string]any{"t": "incense-result", "requestId": requestID, "ok": ok}) {
		slow = append(slow, c)
	}
	h.mu.Unlock()
	closeSlowPeers(slow)
}

func (h *LiveHub) handleIncenseLeave(c *liveClient, turnID string, abandon bool) {
	_ = abandon
	now := time.Now().UnixMilli()
	h.mu.Lock()
	slow := h.expireAndBroadcastLocked(c.room, now)
	q := &h.stateLocked(c.room).queue
	changed := false
	if h.hasClientLocked(c.room, c) {
		if q.owner == c && turnID != "" && turnID == q.turnID {
			changed = h.removeQueueMemberLocked(c.room, c, now)
		} else if q.owner != c && turnID == "" {
			changed = h.removeQueueMemberLocked(c.room, c, now)
		}
	}
	if changed {
		slow = append(slow, h.broadcastIncenseLocked(c.room, now)...)
	}
	h.mu.Unlock()
	closeSlowPeers(slow)
}
func (c *liveClient) handle(h *LiveHub, b []byte) {
	var m struct {
		T          string   `json:"t"`
		X          *float64 `json:"x"`
		Z          *float64 `json:"z"`
		RY         *float64 `json:"ry"`
		Mode       string   `json:"mode"`
		Greet      bool     `json:"greet"`
		Bow        string   `json:"bow"`
		RequestID  int64    `json:"requestId"`
		Candidates *[]int   `json:"candidates"`
		LeaseID    string   `json:"leaseId"`
		TurnID     string   `json:"turnId"`
		Abandon    bool     `json:"abandon"`
	}
	if json.Unmarshal(b, &m) != nil {
		return
	}
	switch m.T {
	case "join":
		// Joining is atomic with the HTTP upgrade; repeated client-side join frames are ignored.
	case "pose":
		h.mu.Lock()
		before := c.peer
		if m.X != nil {
			c.peer.X = clampCoord(*m.X)
		}
		if m.Z != nil {
			c.peer.Z = clampCoord(*m.Z)
		}
		if m.RY != nil {
			c.peer.RY = clampAngle(*m.RY)
		}
		if validLiveMode(m.Mode) {
			c.peer.Mode = m.Mode
		}
		p := c.peer
		h.mu.Unlock()
		if p != before {
			h.broadcast(c.room, map[string]any{"t": "pose", "id": p.ID, "x": p.X, "z": p.Z, "ry": p.RY, "mode": p.Mode}, c)
		}
	case "act":
		h.mu.Lock()
		wasGreet := c.peer.Greet
		wasBow := c.peer.Bow
		c.peer.Greet = m.Greet
		if m.Greet && !wasGreet {
			c.peer.GreetAt = time.Now().UnixMilli()
		} else if !m.Greet {
			c.peer.GreetAt = 0
		}
		if m.Bow == "repeating" || m.Bow == "finishing" || m.Bow == "idle" {
			c.peer.Bow = m.Bow
			if m.Bow == "repeating" && wasBow != "repeating" {
				c.peer.BowAt = time.Now().UnixMilli()
			} else if m.Bow != "repeating" {
				c.peer.BowAt = 0
			}
		}
		p := c.peer
		h.mu.Unlock()
		h.broadcast(c.room, map[string]any{"t": "act", "id": p.ID, "greet": p.Greet, "bow": p.Bow, "greetAt": p.GreetAt, "bowAt": p.BowAt}, c)
	case "seat-request":
		h.handleSeatRequest(c, m.RequestID, m.Candidates)
	case "seat-arrived":
		h.handleSeatArrived(c, m.LeaseID)
	case "seat-release":
		h.handleSeatRelease(c, m.LeaseID)
	case "incense-join":
		h.handleIncenseJoin(c, m.RequestID)
	case "incense-start":
		h.handleIncenseStart(c, m.TurnID)
	case "incense-offer":
		h.handleIncenseOffer(c, m.RequestID, m.TurnID)
	case "incense-leave":
		h.handleIncenseLeave(c, m.TurnID, m.Abandon)
	case "ping":
		_ = c.conn.WriteControl(websocket.PongMessage, nil, time.Now().Add(liveWriteWait))
	}
}

func validLiveMode(mode string) bool {
	switch mode {
	case "walk", "kneel", "beads", "dance":
		return true
	default:
		return false
	}
}

func clampCoord(v float64) float64 {
	if v > 1000 {
		return 1000
	}
	if v < -1000 {
		return -1000
	}
	return v
}
func clampAngle(v float64) float64 {
	return math.Mod(v, 2*math.Pi)
}
func sanitizeID(s string) string {
	if len(s) < 8 || len(s) > 32 {
		return ""
	}
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			continue
		}
		return ""
	}
	return s
}

func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return hex.EncodeToString([]byte(time.Now().Format(time.RFC3339Nano)))[:24]
	}
	return hex.EncodeToString(b[:])
}
