package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func dialLiveTestClient(t *testing.T, serverURL, visitor string) *websocket.Conn {
	t.Helper()
	url := "ws" + strings.TrimPrefix(serverURL, "http") + "/ws?room=maru&visitor=" + visitor
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("connect live visitor %s: %v", visitor, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func readLiveType(t *testing.T, conn *websocket.Conn, want string) map[string]any {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(4 * time.Second))
	for i := 0; i < 32; i++ {
		_, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("waiting for %q: %v", want, err)
		}
		var msg map[string]any
		if err := json.Unmarshal(data, &msg); err != nil {
			t.Fatalf("invalid live JSON %q: %v", data, err)
		}
		if msg["t"] == want {
			return msg
		}
	}
	t.Fatalf("did not receive live message %q", want)
	return nil
}

func sendLive(t *testing.T, conn *websocket.Conn, msg string) {
	t.Helper()
	_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if err := conn.WriteMessage(websocket.TextMessage, []byte(msg)); err != nil {
		t.Fatalf("send live message: %v", err)
	}
}

func connectLiveTestServer(t *testing.T) (*LiveHub, *websocket.Conn, *websocket.Conn) {
	t.Helper()
	hub := NewLiveHub()
	rooms := NewRoomRegistry()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.ServeHTTP(rooms, w, r)
	}))
	t.Cleanup(func() {
		server.Close()
		hub.Close()
	})
	a := dialLiveTestClient(t, server.URL, "visitorA")
	welcome := readLiveType(t, a, "welcome")
	if welcome["serverTime"] == nil {
		t.Fatalf("welcome should include serverTime: %#v", welcome)
	}
	if got := readLiveType(t, a, "seats")["seats"].([]any); len(got) != 0 {
		t.Fatalf("new room should start with empty seat snapshot: %#v", got)
	}
	incense := readLiveType(t, a, "incense-state")
	if incense["turnId"] != "" || len(incense["queue"].(map[string]any)["members"].([]any)) != 0 || len(incense["offerings"].([]any)) != 0 || incense["started"] != nil {
		t.Fatalf("new room should start with empty incense snapshot: %#v", incense)
	}
	b := dialLiveTestClient(t, server.URL, "visitorB")
	readLiveType(t, b, "welcome")
	if got := readLiveType(t, b, "seats")["seats"].([]any); len(got) != 0 {
		t.Fatalf("second connection should receive a seat snapshot: %#v", got)
	}
	readLiveType(t, b, "incense-state")
	return hub, a, b
}

func TestLiveRitualWireWorkflowAndIdempotency(t *testing.T) {
	hub, a, b := connectLiveTestServer(t)

	sendLive(t, a, `{"t":"seat-request","requestId":1,"candidates":[100,5,2]}`)
	seatResult := readLiveType(t, a, "seat-result")
	if seatResult["cushion"] != float64(5) || seatResult["leaseId"] == "" {
		t.Fatalf("seat request should reserve first candidate: %#v", seatResult)
	}
	seats := readLiveType(t, a, "seats")["seats"].([]any)
	if len(seats) != 1 {
		t.Fatalf("expected one reserved seat, got %#v", seats)
	}
	seat := seats[0].(map[string]any)
	if seat["id"] != "visitorA" || seat["cushion"] != float64(5) || seat["status"] != "reserved" {
		t.Fatalf("seat snapshot should identify the owner visitor: %#v", seat)
	}

	sendLive(t, b, `{"t":"seat-request","requestId":1,"candidates":[5]}`)
	if got := readLiveType(t, b, "seat-result"); got["cushion"] != nil {
		t.Fatalf("a second visitor must not reserve the same seat: %#v", got)
	}
	sendLive(t, b, `{"t":"seat-request","requestId":2,"candidates":[]}`)
	if got := readLiveType(t, b, "seat-result"); got["cushion"] != nil {
		t.Fatalf("empty candidates should return null: %#v", got)
	}
	// Reusing a request ID with a different candidate list must replay its original result.
	sendLive(t, b, `{"t":"seat-request","requestId":2,"candidates":[7]}`)
	if got := readLiveType(t, b, "seat-result"); got["cushion"] != nil {
		t.Fatalf("duplicate request ID must be idempotent: %#v", got)
	}
	sendLive(t, b, `{"t":"seat-request","requestId":3,"candidates":null}`)
	if got := readLiveType(t, b, "seat-result"); got["cushion"] != nil {
		t.Fatalf("null candidates should also return null: %#v", got)
	}

	leaseID := seatResult["leaseId"].(string)
	sendLive(t, a, `{"t":"seat-arrived","leaseId":"`+leaseID+`"}`)
	seats = readLiveType(t, a, "seats")["seats"].([]any)
	if seats[0].(map[string]any)["status"] != "occupied" {
		t.Fatalf("seat-arrived should promote the lease: %#v", seats)
	}

	sendLive(t, a, `{"t":"incense-join","requestId":1}`)
	if got := readLiveType(t, a, "incense-result"); got["ok"] != true {
		t.Fatalf("first queue request failed: %#v", got)
	}
	if got := readLiveType(t, a, "seats")["seats"].([]any); len(got) != 0 {
		t.Fatalf("joining incense must release seats, got %#v", got)
	}
	firstState := readLiveType(t, a, "incense-state")
	turnA := firstState["turnId"].(string)
	if turnA == "" {
		t.Fatalf("first queue member should own a turn: %#v", firstState)
	}
	if queue := firstState["queue"].(map[string]any); queue["activeId"] != "visitorA" || len(queue["members"].([]any)) != 1 {
		t.Fatalf("unexpected first queue snapshot: %#v", queue)
	}

	sendLive(t, b, `{"t":"incense-join","requestId":1}`)
	if got := readLiveType(t, b, "incense-result"); got["ok"] != true {
		t.Fatalf("second queue request failed: %#v", got)
	}
	readLiveType(t, b, "incense-state")
	queueState := readLiveType(t, a, "incense-state")
	if len(queueState["queue"].(map[string]any)["members"].([]any)) != 2 {
		t.Fatalf("FIFO queue should contain both visitors: %#v", queueState)
	}

	// A non-active visitor can leave with an empty token; an active owner cannot.
	sendLive(t, b, `{"t":"incense-leave","turnId":"","abandon":true}`)
	queueState = readLiveType(t, a, "incense-state")
	if len(queueState["queue"].(map[string]any)["members"].([]any)) != 1 {
		t.Fatalf("non-active visitor should be removed by empty-token leave: %#v", queueState)
	}
	sendLive(t, b, `{"t":"incense-join","requestId":2}`)
	readLiveType(t, b, "incense-result")
	readLiveType(t, b, "incense-state")
	queueState = readLiveType(t, a, "incense-state")

	sendLive(t, a, `{"t":"incense-start","turnId":"stale-token"}`)
	_ = queueState
	sendLive(t, a, `{"t":"incense-start","turnId":"`+turnA+`"}`)
	started := readLiveType(t, a, "incense-state")
	readLiveType(t, b, "incense-state")
	if started["started"].(map[string]any)["id"] != "visitorA" {
		t.Fatalf("only the active owner should start the ritual: %#v", started)
	}

	sendLive(t, a, `{"t":"incense-offer","requestId":1,"turnId":"`+turnA+`"}`)
	offerState := readLiveType(t, a, "incense-state")
	if got := readLiveType(t, a, "incense-result"); got["ok"] != true {
		t.Fatalf("valid incense offer failed: %#v", got)
	}
	if offers := offerState["offerings"].([]any); len(offers) != 1 {
		t.Fatalf("offer should be recorded exactly once: %#v", offerState)
	}
	readLiveType(t, b, "incense-state")
	sendLive(t, a, `{"t":"incense-offer","requestId":1,"turnId":"`+turnA+`"}`)
	if got := readLiveType(t, a, "incense-result"); got["ok"] != true {
		t.Fatalf("duplicate offer should replay success: %#v", got)
	}
	sendLive(t, a, `{"t":"incense-offer","requestId":2,"turnId":"`+turnA+`"}`)
	if got := readLiveType(t, a, "incense-result"); got["ok"] != false {
		t.Fatalf("second offer in the same turn should fail: %#v", got)
	}

	sendLive(t, a, `{"t":"incense-leave","turnId":"stale-token","abandon":false}`)
	sendLive(t, a, `{"t":"incense-leave","turnId":"`+turnA+`","abandon":false}`)
	nextState := readLiveType(t, a, "incense-state")
	readLiveType(t, b, "incense-state")
	nextTurn := nextState["turnId"].(string)
	if nextTurn == "" || nextTurn == turnA || nextState["queue"].(map[string]any)["activeId"] != "visitorB" {
		t.Fatalf("leaving should advance FIFO to visitorB: %#v", nextState)
	}
	sendLive(t, a, `{"t":"incense-start","turnId":"`+turnA+`"}`)
	hub.mu.RLock()
	finalState := hIncenseSnapshotForTest(hub, "maru")
	hub.mu.RUnlock()
	if finalState["turnId"] != nextTurn || finalState["queue"].(map[string]any)["activeId"] != "visitorB" {
		t.Fatalf("stale token must not mutate the new owner: %#v", finalState)
	}
	sendLive(t, b, `{"t":"incense-leave","turnId":"`+nextTurn+`","abandon":true}`)
	emptyState := readLiveType(t, b, "incense-state")
	if len(emptyState["queue"].(map[string]any)["members"].([]any)) != 0 || emptyState["turnId"] != "" {
		t.Fatalf("queue should become empty after final leave: %#v", emptyState)
	}
	hub.mu.RLock()
	_, roomExists := hub.roomState["maru"]
	hub.mu.RUnlock()
	if !roomExists {
		t.Fatal("room state should remain while visitors are connected")
	}
}

func TestLiveSeatRequestsAreAtomic(t *testing.T) {
	hub, a, b := connectLiveTestServer(t)
	var writers sync.WaitGroup
	writeErrors := make(chan error, 2)
	for _, conn := range []*websocket.Conn{a, b} {
		writers.Add(1)
		go func(conn *websocket.Conn) {
			defer writers.Done()
			writeErrors <- conn.WriteMessage(websocket.TextMessage, []byte(`{"t":"seat-request","requestId":1,"candidates":[12]}`))
		}(conn)
	}
	writers.Wait()
	close(writeErrors)
	for err := range writeErrors {
		if err != nil {
			t.Fatalf("concurrent seat request failed: %v", err)
		}
	}
	ra, rb := readLiveType(t, a, "seat-result"), readLiveType(t, b, "seat-result")
	granted := 0
	for _, result := range []map[string]any{ra, rb} {
		if result["cushion"] == float64(12) && result["leaseId"] != "" {
			granted++
		} else if result["cushion"] != nil {
			t.Fatalf("unexpected concurrent reservation result: %#v", result)
		}
	}
	if granted != 1 {
		t.Fatalf("exactly one connection should reserve the cushion, got %d: %#v %#v", granted, ra, rb)
	}
	hub.mu.RLock()
	count := len(hub.roomState["maru"].seats)
	hub.mu.RUnlock()
	if count != 1 {
		t.Fatalf("atomic requests should leave one lease, got %d", count)
	}
}

func TestLiveDisconnectReleasesSeatAndQueue(t *testing.T) {
	_, a, b := connectLiveTestServer(t)
	sendLive(t, a, `{"t":"seat-request","requestId":1,"candidates":[9]}`)
	readLiveType(t, a, "seat-result")
	if seats := readLiveType(t, a, "seats")["seats"].([]any); len(seats) != 1 {
		t.Fatalf("expected a live reservation before disconnect: %#v", seats)
	}
	readLiveType(t, b, "seats")
	_ = a.Close()
	if seats := readLiveType(t, b, "seats")["seats"].([]any); len(seats) != 0 {
		t.Fatalf("disconnect should reclaim the visitor's seat: %#v", seats)
	}
}

func TestLiveDisconnectAdvancesQueue(t *testing.T) {
	_, a, b := connectLiveTestServer(t)
	sendLive(t, a, `{"t":"incense-join","requestId":1}`)
	readLiveType(t, a, "incense-result")
	readLiveType(t, a, "incense-state")
	readLiveType(t, b, "incense-state")
	_ = a.Close()
	state := readLiveType(t, b, "incense-state")
	if state["turnId"] != "" || len(state["queue"].(map[string]any)["members"].([]any)) != 0 {
		t.Fatalf("disconnect should advance and empty the queue: %#v", state)
	}
}

func hIncenseSnapshotForTest(h *LiveHub, room string) map[string]any {
	b, _ := json.Marshal(h.incenseSnapshotLocked(room, time.Now().UnixMilli()))
	var snapshot map[string]any
	_ = json.Unmarshal(b, &snapshot)
	return snapshot
}

func TestLiveExpiryReclaimsReservationsAndAdvancesQueue(t *testing.T) {
	hub := NewLiveHub()
	t.Cleanup(hub.Close)
	now := time.Now().UnixMilli()
	a := &liveClient{peer: livePeer{ID: "visitorA"}, send: make(chan []byte, liveQueue)}
	b := &liveClient{peer: livePeer{ID: "visitorB"}, send: make(chan []byte, liveQueue)}
	hub.mu.Lock()
	hub.rooms["maru"] = map[*liveClient]struct{}{a: {}, b: {}}
	s := hub.stateLocked("maru")
	s.seats[1] = &seatLease{leaseID: "reserved", cushion: 1, owner: a, visitorID: a.peer.ID, status: "reserved", expiresAt: now - 1}
	s.seats[2] = &seatLease{leaseID: "occupied", cushion: 2, owner: b, visitorID: b.peer.ID, status: "occupied"}
	s.queue = incenseQueueState{revision: 5, members: []string{a.peer.ID, b.peer.ID}, owner: a, turnID: "old-turn", expiresAt: now - 1, started: &incenseStarted{ID: a.peer.ID, At: now - 1000}}
	s.offerings = []incenseOffering{{ID: "old-offer", VisitorID: a.peer.ID, At: now - 2000, ExpiresAt: now - 1}}
	seatsChanged, incenseChanged := hub.expireRoomLocked("maru", now)
	if !seatsChanged || !incenseChanged {
		hub.mu.Unlock()
		t.Fatalf("expiry should report reclaimed seat and incense state: %v %v", seatsChanged, incenseChanged)
	}
	if len(s.seats) != 1 || s.seats[2].status != "occupied" {
		hub.mu.Unlock()
		t.Fatalf("only expired reserved seats should be reclaimed: %#v", s.seats)
	}
	if s.queue.owner != b || s.queue.turnID == "" || s.queue.turnID == "old-turn" || s.queue.expiresAt <= now || s.queue.started != nil {
		hub.mu.Unlock()
		t.Fatalf("expired active turn should advance and reset: %#v", s.queue)
	}
	if len(s.offerings) != 0 {
		hub.mu.Unlock()
		t.Fatalf("expired offerings should be removed: %#v", s.offerings)
	}
	hub.mu.Unlock()
}

func TestLiveRequestCacheBounded(t *testing.T) {
	cache := make(map[int64]bool)
	for id := int64(1); id <= 1000; id++ {
		cacheLiveRequest(cache, id, true)
	}
	if len(cache) > 64 {
		t.Fatalf("request cache grew beyond bound: %d", len(cache))
	}
}
