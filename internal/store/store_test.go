package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/fujr/connection_recorder/internal/collector"
)

func TestUpsertDeduplicatesConnections(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "networkmon.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	now := time.Now()
	conn := collector.Connection{
		ObservedAt: now,
		Proto:      "tcp",
		State:      "ESTABLISHED",
		LocalIP:    "127.0.0.1",
		LocalPort:  50000,
		RemoteIP:   "127.0.0.1",
		RemotePort: 443,
		SourceIP:   "127.0.0.1",
		SourcePort: 50000,
		DestIP:     "127.0.0.1",
		DestPort:   443,
		Inode:      "1",
		NetNS:      "net:[4026531993]",
		PID:        123,
		Process:    "curl",
		Exe:        "/usr/bin/curl",
		UID:        0,
		Direction:  "outbound",
		Cgroup:     "0::/system.slice/docker-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.scope",
		Container:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	if err := st.UpsertConnections(context.Background(), []collector.Connection{conn, conn}); err != nil {
		t.Fatal(err)
	}
	records, err := st.List(context.Background(), Query{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("records = %d", len(records))
	}
	if records[0].SeenCount != 2 {
		t.Fatalf("seen_count = %d", records[0].SeenCount)
	}
	if records[0].NetNS != "net:[4026531993]" {
		t.Fatalf("netns = %s", records[0].NetNS)
	}
	if records[0].Container != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("container = %s", records[0].Container)
	}
}

func TestListFiltersSourceAndDest(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "networkmon.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	conn := collector.Connection{
		ObservedAt: time.Now(),
		Proto:      "tcp",
		State:      "ESTABLISHED",
		LocalIP:    "192.168.1.10",
		LocalPort:  51000,
		RemoteIP:   "8.8.8.8",
		RemotePort: 443,
		SourceIP:   "192.168.1.10",
		SourcePort: 51000,
		DestIP:     "8.8.8.8",
		DestPort:   443,
		Inode:      "conntrack",
		NetNS:      "conntrack",
		Direction:  "forward",
		Process:    "conntrack",
	}
	if err := st.UpsertConnections(context.Background(), []collector.Connection{conn}); err != nil {
		t.Fatal(err)
	}
	records, err := st.List(context.Background(), Query{Source: "192.168.1.10", Dest: "8.8.8.8"})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("records = %d", len(records))
	}
	if records[0].Direction != "forward" {
		t.Fatalf("direction = %s", records[0].Direction)
	}
}
