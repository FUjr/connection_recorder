package collector

import "testing"

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
