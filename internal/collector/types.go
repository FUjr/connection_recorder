package collector

import "time"

type Connection struct {
	ObservedAt time.Time
	Proto      string
	State      string
	LocalIP    string
	LocalPort  int
	RemoteIP   string
	RemotePort int
	SourceIP   string
	SourcePort int
	DestIP     string
	DestPort   int
	Inode      string
	NetNS      string
	PID        int
	Process    string
	Exe        string
	UID        int
	Direction  string
	Cgroup     string
	Container  string
}
