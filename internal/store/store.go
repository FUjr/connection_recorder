package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/fujr/connection_recorder/internal/collector"
)

type Store struct {
	db *sql.DB
}

type Query struct {
	Since     time.Duration
	Limit     int
	PID       int
	Process   string
	Remote    string
	Container string
	NetNS     string
}

type Record struct {
	ID         int64     `json:"id"`
	FirstSeen  time.Time `json:"first_seen"`
	LastSeen   time.Time `json:"last_seen"`
	SeenCount  int64     `json:"seen_count"`
	Proto      string    `json:"proto"`
	State      string    `json:"state"`
	LocalIP    string    `json:"local_ip"`
	LocalPort  int       `json:"local_port"`
	RemoteIP   string    `json:"remote_ip"`
	RemotePort int       `json:"remote_port"`
	Inode      string    `json:"inode"`
	NetNS      string    `json:"netns"`
	PID        int       `json:"pid"`
	Process    string    `json:"process"`
	Exe        string    `json:"exe"`
	UID        int       `json:"uid"`
	Direction  string    `json:"direction"`
	Cgroup     string    `json:"cgroup"`
	Container  string    `json:"container_id"`
}

type Stats struct {
	Records24h int64 `json:"records_24h"`
	RecordsAll int64 `json:"records_all"`
	DBBytes    int64 `json:"db_bytes"`
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite3", path+"?_busy_timeout=5000&_journal_mode=WAL")
	if err != nil {
		return nil, err
	}
	store := &Store{db: db}
	if err := store.init(); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) init() error {
	stmts := []string{
		`PRAGMA journal_mode=WAL`,
		`PRAGMA synchronous=NORMAL`,
		`CREATE TABLE IF NOT EXISTS connections (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			fingerprint TEXT NOT NULL UNIQUE,
			first_seen INTEGER NOT NULL,
			last_seen INTEGER NOT NULL,
			seen_count INTEGER NOT NULL DEFAULT 1,
			proto TEXT NOT NULL,
			state TEXT NOT NULL,
			local_ip TEXT NOT NULL,
			local_port INTEGER NOT NULL,
			remote_ip TEXT NOT NULL,
			remote_port INTEGER NOT NULL,
			inode TEXT NOT NULL,
			netns TEXT NOT NULL DEFAULT '',
			pid INTEGER NOT NULL,
			process TEXT NOT NULL,
			exe TEXT NOT NULL,
			uid INTEGER NOT NULL,
			direction TEXT NOT NULL,
			cgroup TEXT NOT NULL DEFAULT '',
			container_id TEXT NOT NULL DEFAULT ''
		)`,
		`ALTER TABLE connections ADD COLUMN netns TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE connections ADD COLUMN cgroup TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE connections ADD COLUMN container_id TEXT NOT NULL DEFAULT ''`,
		`CREATE INDEX IF NOT EXISTS idx_connections_last_seen ON connections(last_seen)`,
		`CREATE INDEX IF NOT EXISTS idx_connections_pid ON connections(pid)`,
		`CREATE INDEX IF NOT EXISTS idx_connections_process ON connections(process)`,
		`CREATE INDEX IF NOT EXISTS idx_connections_remote ON connections(remote_ip, remote_port)`,
		`CREATE INDEX IF NOT EXISTS idx_connections_container ON connections(container_id)`,
		`CREATE INDEX IF NOT EXISTS idx_connections_netns ON connections(netns)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			if strings.Contains(err.Error(), "duplicate column name") {
				continue
			}
			return err
		}
	}
	return nil
}

func (s *Store) UpsertConnections(ctx context.Context, conns []collector.Connection) error {
	if len(conns) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `INSERT INTO connections (
		fingerprint, first_seen, last_seen, seen_count, proto, state, local_ip, local_port,
		remote_ip, remote_port, inode, netns, pid, process, exe, uid, direction, cgroup, container_id
	) VALUES (?, ?, ?, 1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(fingerprint) DO UPDATE SET
		last_seen=excluded.last_seen,
		seen_count=connections.seen_count + 1,
		state=excluded.state,
		inode=excluded.inode,
		netns=excluded.netns,
		process=excluded.process,
		exe=excluded.exe,
		uid=excluded.uid,
		direction=excluded.direction,
		cgroup=excluded.cgroup,
		container_id=excluded.container_id`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, conn := range conns {
		_, err := stmt.ExecContext(ctx,
			fingerprint(conn),
			conn.ObservedAt.Unix(),
			conn.ObservedAt.Unix(),
			conn.Proto,
			conn.State,
			conn.LocalIP,
			conn.LocalPort,
			conn.RemoteIP,
			conn.RemotePort,
			conn.Inode,
			conn.NetNS,
			conn.PID,
			conn.Process,
			conn.Exe,
			conn.UID,
			conn.Direction,
			conn.Cgroup,
			conn.Container,
		)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) List(ctx context.Context, q Query) ([]Record, error) {
	if q.Limit <= 0 {
		q.Limit = 100
	}
	if q.Limit > 10000 {
		q.Limit = 10000
	}
	var args []any
	conditions := []string{"1=1"}
	if q.Since > 0 {
		conditions = append(conditions, "last_seen >= ?")
		args = append(args, time.Now().Add(-q.Since).Unix())
	}
	if q.PID > 0 {
		conditions = append(conditions, "pid = ?")
		args = append(args, q.PID)
	}
	if q.Process != "" {
		conditions = append(conditions, "process LIKE ?")
		args = append(args, "%"+q.Process+"%")
	}
	if q.Remote != "" {
		conditions = append(conditions, "(remote_ip = ? OR remote_ip LIKE ?)")
		args = append(args, q.Remote, "%"+q.Remote+"%")
	}
	if q.Container != "" {
		conditions = append(conditions, "container_id LIKE ?")
		args = append(args, q.Container+"%")
	}
	if q.NetNS != "" {
		conditions = append(conditions, "netns = ?")
		args = append(args, q.NetNS)
	}
	args = append(args, q.Limit)

	rows, err := s.db.QueryContext(ctx, `SELECT id, first_seen, last_seen, seen_count, proto, state,
		local_ip, local_port, remote_ip, remote_port, inode, netns, pid, process, exe, uid,
		direction, cgroup, container_id
		FROM connections WHERE `+strings.Join(conditions, " AND ")+`
		ORDER BY last_seen DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []Record
	for rows.Next() {
		var r Record
		var first, last int64
		if err := rows.Scan(&r.ID, &first, &last, &r.SeenCount, &r.Proto, &r.State,
			&r.LocalIP, &r.LocalPort, &r.RemoteIP, &r.RemotePort, &r.Inode, &r.NetNS, &r.PID,
			&r.Process, &r.Exe, &r.UID, &r.Direction, &r.Cgroup, &r.Container); err != nil {
			return nil, err
		}
		r.FirstSeen = time.Unix(first, 0)
		r.LastSeen = time.Unix(last, 0)
		records = append(records, r)
	}
	return records, rows.Err()
}

func (s *Store) Prune(ctx context.Context, retention time.Duration) (int64, error) {
	cutoff := time.Now().Add(-retention).Unix()
	result, err := s.db.ExecContext(ctx, `DELETE FROM connections WHERE last_seen < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	rows, _ := result.RowsAffected()
	return rows, nil
}

func (s *Store) Stats(ctx context.Context, dbPath string) (Stats, error) {
	var stats Stats
	dayAgo := time.Now().Add(-24 * time.Hour).Unix()
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM connections WHERE last_seen >= ?`, dayAgo).Scan(&stats.Records24h); err != nil {
		return stats, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM connections`).Scan(&stats.RecordsAll); err != nil {
		return stats, err
	}
	stats.DBBytes = fileSize(dbPath)
	if wal := fileSize(dbPath + "-wal"); wal > 0 {
		stats.DBBytes += wal
	}
	return stats, nil
}

func fingerprint(c collector.Connection) string {
	raw := fmt.Sprintf("%s|%s|%s|%d|%s|%d|%d|%s|%d",
		c.NetNS, c.Proto, c.LocalIP, c.LocalPort, c.RemoteIP, c.RemotePort, c.PID, c.Exe, c.UID)
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}
