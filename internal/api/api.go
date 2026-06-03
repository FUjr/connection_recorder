package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"

	"github.com/fujr/connection_recorder/internal/config"
	"github.com/fujr/connection_recorder/internal/store"
)

type Request struct {
	Command   string `json:"command"`
	Since     string `json:"since,omitempty"`
	Limit     int    `json:"limit,omitempty"`
	PID       int    `json:"pid,omitempty"`
	Process   string `json:"process,omitempty"`
	Remote    string `json:"remote,omitempty"`
	Source    string `json:"source,omitempty"`
	Dest      string `json:"dest,omitempty"`
	Container string `json:"container,omitempty"`
	NetNS     string `json:"netns,omitempty"`
	Interval  string `json:"interval,omitempty"`
	Retention string `json:"retention,omitempty"`
	DBPath    string `json:"db_path,omitempty"`
}

type Response struct {
	OK      bool              `json:"ok"`
	Error   string            `json:"error,omitempty"`
	Config  config.WireConfig `json:"config,omitempty"`
	Status  *Status           `json:"status,omitempty"`
	Records []store.Record    `json:"records,omitempty"`
	Pruned  int64             `json:"pruned,omitempty"`
}

type Status struct {
	Config          config.WireConfig `json:"config"`
	Stats           store.Stats       `json:"stats"`
	LastCollectTime string            `json:"last_collect_time"`
	LastCollectUnix int64             `json:"last_collect_unix"`
	LastCollectErr  string            `json:"last_collect_error,omitempty"`
	LastCount       int               `json:"last_count"`
}

func Send(socketPath string, req Request) (Response, error) {
	conn, err := net.DialTimeout("unix", socketPath, 5*time.Second)
	if err != nil {
		return Response{}, err
	}
	defer conn.Close()
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return Response{}, err
	}
	if unix, ok := conn.(*net.UnixConn); ok {
		_ = unix.CloseWrite()
	}
	data, err := io.ReadAll(conn)
	if err != nil {
		return Response{}, err
	}
	var resp Response
	if err := json.Unmarshal(data, &resp); err != nil {
		return Response{}, err
	}
	if !resp.OK {
		return resp, fmt.Errorf("%s", resp.Error)
	}
	return resp, nil
}

func ParseQuery(req Request) (store.Query, error) {
	var q store.Query
	q.Limit = req.Limit
	q.PID = req.PID
	q.Process = req.Process
	q.Remote = req.Remote
	q.Source = req.Source
	q.Dest = req.Dest
	q.Container = req.Container
	q.NetNS = req.NetNS
	if req.Since != "" {
		since, err := time.ParseDuration(req.Since)
		if err != nil {
			return q, err
		}
		q.Since = since
	}
	return q, nil
}

func ParseDurationFlag(value string, name string) (time.Duration, error) {
	if value == "" {
		return 0, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("invalid %s %q: %w", name, value, err)
	}
	return parsed, nil
}

func Atoi(value, name string) (int, error) {
	if value == "" {
		return 0, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("invalid %s %q: %w", name, value, err)
	}
	return parsed, nil
}
