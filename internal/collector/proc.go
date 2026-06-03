package collector

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Collector struct {
	ProcRoot string
}

type procInfo struct {
	PID       int
	Process   string
	Exe       string
	NetNS     string
	Cgroup    string
	Container string
}

type procEntry struct {
	pid  int
	name string
	info procInfo
}

type socketTable struct {
	byInode map[string]procInfo
}

type namespaceInfo struct {
	ID        string
	PIDs      []int
	Cgroup    string
	Container string
}

var containerIDPattern = regexp.MustCompile(`(?i)[0-9a-f]{64}`)

func New() *Collector {
	return &Collector{ProcRoot: "/proc"}
}

func (c *Collector) Snapshot(now time.Time) ([]Connection, error) {
	processes := c.processEntries()
	sockets := c.socketOwners(processes)
	namespaces := netNamespaces(processes)
	files := []struct {
		path  string
		proto string
		ipv6  bool
	}{
		{"net/tcp", "tcp", false},
		{"net/tcp6", "tcp6", true},
		{"net/udp", "udp", false},
		{"net/udp6", "udp6", true},
	}

	var out []Connection
	for _, ns := range namespaces {
		for _, file := range files {
			conns, err := c.parseNamespaceNetFile(ns, file.path, file.proto, file.ipv6, sockets, now)
			if err != nil && !errors.Is(err, os.ErrNotExist) && !errors.Is(err, os.ErrPermission) {
				return nil, err
			}
			out = append(out, conns...)
		}
	}
	forwardConns, err := c.parseConntrack(now)
	if err != nil && !errors.Is(err, os.ErrNotExist) && !errors.Is(err, os.ErrPermission) {
		return nil, err
	}
	out = append(out, forwardConns...)
	return out, nil
}

func (c *Collector) processEntries() []procEntry {
	entries, err := os.ReadDir(c.ProcRoot)
	if err != nil {
		return nil
	}

	var processes []procEntry
	for _, entry := range entries {
		if !entry.IsDir() || !isDigits(entry.Name()) {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		netNS := readlink(filepath.Join(c.ProcRoot, entry.Name(), "ns", "net"))
		if netNS == "" {
			continue
		}
		cgroup := readCgroup(filepath.Join(c.ProcRoot, entry.Name(), "cgroup"))
		processes = append(processes, procEntry{
			pid:  pid,
			name: entry.Name(),
			info: procInfo{
				PID:       pid,
				Process:   readComm(filepath.Join(c.ProcRoot, entry.Name(), "comm")),
				Exe:       readlink(filepath.Join(c.ProcRoot, entry.Name(), "exe")),
				NetNS:     netNS,
				Cgroup:    cgroup,
				Container: extractContainerID(cgroup),
			},
		})
	}
	return processes
}

func (c *Collector) socketOwners(processes []procEntry) socketTable {
	table := socketTable{byInode: make(map[string]procInfo)}

	for _, process := range processes {
		fdDir := filepath.Join(c.ProcRoot, process.name, "fd")
		fds, err := os.ReadDir(fdDir)
		if err != nil {
			continue
		}
		for _, fd := range fds {
			target, err := os.Readlink(filepath.Join(fdDir, fd.Name()))
			if err != nil {
				continue
			}
			inode, ok := socketInode(target)
			if ok {
				table.byInode[inode] = process.info
			}
		}
	}
	return table
}

func netNamespaces(processes []procEntry) []namespaceInfo {
	byID := make(map[string]int)
	var namespaces []namespaceInfo
	for _, process := range processes {
		idx, ok := byID[process.info.NetNS]
		if !ok {
			namespaces = append(namespaces, namespaceInfo{ID: process.info.NetNS})
			idx = len(namespaces) - 1
			byID[process.info.NetNS] = idx
		}
		ns := &namespaces[idx]
		ns.PIDs = append(ns.PIDs, process.pid)
		if ns.Container == "" && process.info.Container != "" {
			ns.Container = process.info.Container
			ns.Cgroup = process.info.Cgroup
		}
	}
	return namespaces
}

func (c *Collector) parseNamespaceNetFile(ns namespaceInfo, relPath, proto string, ipv6 bool, sockets socketTable, now time.Time) ([]Connection, error) {
	var lastErr error
	for _, pid := range ns.PIDs {
		path := filepath.Join(c.ProcRoot, strconv.Itoa(pid), relPath)
		conns, err := c.parseNetFile(path, ns, proto, ipv6, sockets, now)
		if err == nil {
			return conns, nil
		}
		lastErr = err
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) {
			continue
		}
	}
	return nil, lastErr
}

func (c *Collector) parseNetFile(path string, ns namespaceInfo, proto string, ipv6 bool, sockets socketTable, now time.Time) ([]Connection, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var conns []Connection
	scanner := bufio.NewScanner(file)
	first := true
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if first {
			first = false
			continue
		}
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 10 {
			continue
		}
		state := socketState(fields[3], proto)
		if state == "LISTEN" {
			continue
		}
		localIP, localPort, err := parseAddr(fields[1], ipv6)
		if err != nil {
			continue
		}
		remoteIP, remotePort, err := parseAddr(fields[2], ipv6)
		if err != nil {
			continue
		}
		uid, _ := strconv.Atoi(fields[7])
		inode := fields[9]
		owner := sockets.byInode[inode]
		cgroup := owner.Cgroup
		container := owner.Container
		if container == "" {
			container = ns.Container
			cgroup = ns.Cgroup
		}
		direction := inferDirection(localIP, localPort, remoteIP, remotePort, state)
		sourceIP, sourcePort, destIP, destPort := endpoints(localIP, localPort, remoteIP, remotePort, direction)
		conns = append(conns, Connection{
			ObservedAt: now,
			Proto:      proto,
			State:      state,
			LocalIP:    localIP,
			LocalPort:  localPort,
			RemoteIP:   remoteIP,
			RemotePort: remotePort,
			SourceIP:   sourceIP,
			SourcePort: sourcePort,
			DestIP:     destIP,
			DestPort:   destPort,
			Inode:      inode,
			NetNS:      ns.ID,
			PID:        owner.PID,
			Process:    owner.Process,
			Exe:        owner.Exe,
			UID:        uid,
			Direction:  direction,
			Cgroup:     cgroup,
			Container:  container,
		})
	}
	return conns, scanner.Err()
}

func (c *Collector) parseConntrack(now time.Time) ([]Connection, error) {
	paths := []string{
		filepath.Join(c.ProcRoot, "net", "nf_conntrack"),
		filepath.Join(c.ProcRoot, "net", "ip_conntrack"),
	}
	var lastErr error
	for _, path := range paths {
		conns, err := c.parseConntrackFile(path, now)
		if err == nil {
			return conns, nil
		}
		lastErr = err
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) {
			continue
		}
		return nil, err
	}
	return nil, lastErr
}

func (c *Collector) parseConntrackFile(path string, now time.Time) ([]Connection, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var conns []Connection
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		conn, ok := parseConntrackLine(scanner.Text(), now)
		if ok {
			conns = append(conns, conn)
		}
	}
	return conns, scanner.Err()
}

func parseConntrackLine(line string, now time.Time) (Connection, bool) {
	fields := strings.Fields(line)
	if len(fields) < 5 {
		return Connection{}, false
	}
	proto := fields[0]
	if proto == "ipv4" || proto == "ipv6" {
		if len(fields) < 6 {
			return Connection{}, false
		}
		proto = fields[2]
	}
	if proto != "tcp" && proto != "udp" {
		return Connection{}, false
	}

	state := ""
	values := make(map[string]string)
	for _, field := range fields {
		if !strings.Contains(field, "=") {
			if state == "" && isConntrackState(field) {
				state = field
			}
			continue
		}
		parts := strings.SplitN(field, "=", 2)
		if _, exists := values[parts[0]]; !exists {
			values[parts[0]] = parts[1]
		}
	}
	if state == "" {
		state = "TRACKED"
	}

	src := values["src"]
	dst := values["dst"]
	if src == "" || dst == "" {
		return Connection{}, false
	}
	sport, _ := strconv.Atoi(values["sport"])
	dport, _ := strconv.Atoi(values["dport"])
	if sport == 0 && dport == 0 {
		return Connection{}, false
	}

	return Connection{
		ObservedAt: now,
		Proto:      proto,
		State:      state,
		LocalIP:    src,
		LocalPort:  sport,
		RemoteIP:   dst,
		RemotePort: dport,
		SourceIP:   src,
		SourcePort: sport,
		DestIP:     dst,
		DestPort:   dport,
		Inode:      "conntrack",
		Direction:  "forward",
		Process:    "conntrack",
		NetNS:      "conntrack",
		Container:  "",
		Cgroup:     "",
		PID:        0,
		UID:        0,
	}, true
}

func parseAddr(raw string, ipv6 bool) (string, int, error) {
	parts := strings.Split(raw, ":")
	if len(parts) != 2 {
		return "", 0, fmt.Errorf("bad address %q", raw)
	}
	port64, err := strconv.ParseUint(parts[1], 16, 16)
	if err != nil {
		return "", 0, err
	}
	if ipv6 {
		ip, err := parseIPv6(parts[0])
		if err != nil {
			return "", 0, err
		}
		return ip.String(), int(port64), nil
	}
	ip, err := parseIPv4(parts[0])
	if err != nil {
		return "", 0, err
	}
	return ip.String(), int(port64), nil
}

func parseIPv4(hexIP string) (net.IP, error) {
	value, err := strconv.ParseUint(hexIP, 16, 32)
	if err != nil {
		return nil, err
	}
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], uint32(value))
	return net.IPv4(b[0], b[1], b[2], b[3]), nil
}

func parseIPv6(hexIP string) (net.IP, error) {
	if len(hexIP) != 32 {
		return nil, fmt.Errorf("bad ipv6 length")
	}
	ip := make(net.IP, 16)
	for i := 0; i < 16; i += 4 {
		chunk, err := strconv.ParseUint(hexIP[i*2:i*2+8], 16, 32)
		if err != nil {
			return nil, err
		}
		binary.LittleEndian.PutUint32(ip[i:i+4], uint32(chunk))
	}
	return ip, nil
}

func socketState(hexState string, proto string) string {
	if strings.HasPrefix(proto, "udp") {
		if strings.EqualFold(hexState, "07") {
			return "UNCONN"
		}
		if strings.EqualFold(hexState, "01") {
			return "ESTABLISHED"
		}
		return hexState
	}
	states := map[string]string{
		"01": "ESTABLISHED",
		"02": "SYN_SENT",
		"03": "SYN_RECV",
		"04": "FIN_WAIT1",
		"05": "FIN_WAIT2",
		"06": "TIME_WAIT",
		"07": "CLOSE",
		"08": "CLOSE_WAIT",
		"09": "LAST_ACK",
		"0A": "LISTEN",
		"0B": "CLOSING",
	}
	if state, ok := states[strings.ToUpper(hexState)]; ok {
		return state
	}
	return hexState
}

func socketInode(target string) (string, bool) {
	if !strings.HasPrefix(target, "socket:[") || !strings.HasSuffix(target, "]") {
		return "", false
	}
	inode := strings.TrimSuffix(strings.TrimPrefix(target, "socket:["), "]")
	return inode, inode != ""
}

func readComm(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func readlink(path string) string {
	target, err := os.Readlink(path)
	if err != nil {
		return ""
	}
	return target
}

func readCgroup(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func extractContainerID(cgroup string) string {
	if cgroup == "" {
		return ""
	}
	match := containerIDPattern.FindString(cgroup)
	return strings.ToLower(match)
}

func isDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func inferDirection(localIP string, localPort int, remoteIP string, remotePort int, state string) string {
	if remoteIP == "0.0.0.0" || remoteIP == "::" || remotePort == 0 {
		return "local"
	}
	if state == "SYN_SENT" {
		return "outbound"
	}
	if isLikelyEphemeral(localPort) && !isLikelyEphemeral(remotePort) {
		return "outbound"
	}
	if !isLikelyEphemeral(localPort) && isLikelyEphemeral(remotePort) {
		return "inbound"
	}
	return "unknown"
}

func endpoints(localIP string, localPort int, remoteIP string, remotePort int, direction string) (string, int, string, int) {
	if direction == "inbound" {
		return remoteIP, remotePort, localIP, localPort
	}
	return localIP, localPort, remoteIP, remotePort
}

func isLikelyEphemeral(port int) bool {
	return port >= 32768
}

func isConntrackState(value string) bool {
	switch value {
	case "SYN_SENT", "SYN_RECV", "ESTABLISHED", "FIN_WAIT", "CLOSE_WAIT", "LAST_ACK",
		"TIME_WAIT", "CLOSE", "LISTEN", "UNREPLIED", "ASSURED":
		return true
	default:
		return false
	}
}
