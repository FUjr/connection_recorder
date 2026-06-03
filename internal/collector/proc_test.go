package collector

import (
	"testing"
	"time"
)

func TestParseIPv4ProcAddress(t *testing.T) {
	ip, port, err := parseAddr("0100007F:1F90", false)
	if err != nil {
		t.Fatal(err)
	}
	if ip != "127.0.0.1" {
		t.Fatalf("ip = %s", ip)
	}
	if port != 8080 {
		t.Fatalf("port = %d", port)
	}
}

func TestSocketInode(t *testing.T) {
	inode, ok := socketInode("socket:[12345]")
	if !ok {
		t.Fatal("expected socket inode")
	}
	if inode != "12345" {
		t.Fatalf("inode = %s", inode)
	}
}

func TestUDPState(t *testing.T) {
	if state := socketState("07", "udp"); state != "UNCONN" {
		t.Fatalf("state = %s", state)
	}
	if state := socketState("01", "udp"); state != "ESTABLISHED" {
		t.Fatalf("state = %s", state)
	}
}

func TestExtractContainerID(t *testing.T) {
	cgroup := "0::/system.slice/docker-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.scope"
	id := extractContainerID(cgroup)
	if id != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("container id = %s", id)
	}
}

func TestParseConntrackLine(t *testing.T) {
	now := time.Unix(100, 0)
	line := "ipv4 2 tcp 6 431999 ESTABLISHED src=192.168.1.10 dst=8.8.8.8 sport=51000 dport=443 src=8.8.8.8 dst=203.0.113.2 sport=443 dport=51000 [ASSURED] mark=0 use=1"
	conn, ok := parseConntrackLine(line, now)
	if !ok {
		t.Fatal("expected conntrack connection")
	}
	if conn.Direction != "forward" {
		t.Fatalf("direction = %s", conn.Direction)
	}
	if conn.SourceIP != "192.168.1.10" || conn.SourcePort != 51000 {
		t.Fatalf("source = %s:%d", conn.SourceIP, conn.SourcePort)
	}
	if conn.DestIP != "8.8.8.8" || conn.DestPort != 443 {
		t.Fatalf("dest = %s:%d", conn.DestIP, conn.DestPort)
	}
	if conn.Proto != "tcp" || conn.State != "ESTABLISHED" {
		t.Fatalf("proto/state = %s/%s", conn.Proto, conn.State)
	}
}
