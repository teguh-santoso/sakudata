package tenant

import (
	"testing"
	"time"
)

func TestHubBroadcast(t *testing.T) {
	h := NewHub()
	ch1, unsub1 := h.Subscribe("db1", "col1")
	defer unsub1()
	ch2, unsub2 := h.Subscribe("db1", "col1")
	defer unsub2()
	other, unsubOther := h.Subscribe("db1", "lain")
	defer unsubOther()

	e := Event{Event: "create", DB: "db1", Collection: "col1", Record: map[string]any{"id": "x"}}
	h.Broadcast(e)

	for i, ch := range []chan Event{ch1, ch2} {
		select {
		case got := <-ch:
			if got.Collection != "col1" || got.Event != "create" {
				t.Fatalf("sub %d got wrong event: %+v", i, got)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("sub %d received nothing", i)
		}
	}
	select {
	case <-other:
		t.Fatal("unrelated collection received event")
	case <-time.After(100 * time.Millisecond):
	}

	unsub1()
	h.Broadcast(e)
	select {
	case got := <-ch1:
		// Closed channels return the zero value immediately; a real
		// delivery would carry a non-empty event name.
		if got.Event != "" {
			t.Fatalf("unsubscribed channel received event: %+v", got)
		}
	case <-time.After(100 * time.Millisecond):
	}
}
