package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/fujr/connection_recorder/internal/api"
	"github.com/fujr/connection_recorder/internal/config"
	"github.com/fujr/connection_recorder/internal/store"
)

func main() {
	socket := flag.String("socket", config.DefaultSocket, "networkmond Unix socket")
	flag.Usage = usage
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}

	var err error
	switch args[0] {
	case "status":
		err = status(*socket)
	case "list":
		err = list(*socket, args[1:])
	case "config":
		err = configCmd(*socket, args[1:])
	case "prune":
		err = prune(*socket)
	default:
		err = fmt.Errorf("unknown command %q", args[0])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "networkmonc:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `Usage:
  networkmonc [--socket PATH] status
  networkmonc [--socket PATH] list [--since 1h] [--limit 100] [--pid 123] [--process NAME] [--remote IP] [--container ID] [--netns NS] [--json]
  networkmonc [--socket PATH] config get
  networkmonc [--socket PATH] config set [--interval 500ms] [--retention 24h] [--db /opt/networkmon/networkmon.db]
  networkmonc [--socket PATH] prune
`)
}

func status(socket string) error {
	resp, err := api.Send(socket, api.Request{Command: "status"})
	if err != nil {
		return err
	}
	s := resp.Status
	if s == nil {
		return fmt.Errorf("empty status response")
	}
	fmt.Printf("socket: %s\n", s.Config.Socket)
	fmt.Printf("database: %s\n", s.Config.DBPath)
	fmt.Printf("interval: %s\n", s.Config.Interval)
	fmt.Printf("retention: %s\n", s.Config.Retention)
	fmt.Printf("last_collect: %s\n", s.LastCollectTime)
	fmt.Printf("last_count: %d\n", s.LastCount)
	if s.LastCollectErr != "" {
		fmt.Printf("last_error: %s\n", s.LastCollectErr)
	}
	fmt.Printf("records_24h: %d\n", s.Stats.Records24h)
	fmt.Printf("records_all: %d\n", s.Stats.RecordsAll)
	fmt.Printf("db_bytes: %d\n", s.Stats.DBBytes)
	return nil
}

func list(socket string, args []string) error {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	since := fs.String("since", "", "only show records seen within this duration")
	limit := fs.Int("limit", 100, "maximum records")
	pid := fs.Int("pid", 0, "filter by PID")
	process := fs.String("process", "", "filter by process name")
	remote := fs.String("remote", "", "filter by remote IP")
	container := fs.String("container", "", "filter by container ID prefix")
	netns := fs.String("netns", "", "filter by network namespace")
	jsonOut := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	resp, err := api.Send(socket, api.Request{
		Command:   "list",
		Since:     *since,
		Limit:     *limit,
		PID:       *pid,
		Process:   *process,
		Remote:    *remote,
		Container: *container,
		NetNS:     *netns,
	})
	if err != nil {
		return err
	}
	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(resp.Records)
	}
	printRecords(resp.Records)
	return nil
}

func configCmd(socket string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("config requires get or set")
	}
	switch args[0] {
	case "get":
		resp, err := api.Send(socket, api.Request{Command: "config_get"})
		if err != nil {
			return err
		}
		return printJSON(resp.Config)
	case "set":
		fs := flag.NewFlagSet("config set", flag.ExitOnError)
		interval := fs.String("interval", "", "poll interval")
		retention := fs.String("retention", "", "record retention")
		dbPath := fs.String("db", "", "SQLite database path")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *interval == "" && *retention == "" && *dbPath == "" {
			return fmt.Errorf("nothing to set")
		}
		resp, err := api.Send(socket, api.Request{
			Command:   "config_set",
			Interval:  *interval,
			Retention: *retention,
			DBPath:    *dbPath,
		})
		if err != nil {
			return err
		}
		return printJSON(resp.Config)
	default:
		return fmt.Errorf("unknown config command %q", args[0])
	}
}

func prune(socket string) error {
	resp, err := api.Send(socket, api.Request{Command: "prune"})
	if err != nil {
		return err
	}
	fmt.Printf("pruned: %d\n", resp.Pruned)
	return nil
}

func printRecords(records []store.Record) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "LAST_SEEN\tCOUNT\tPROTO\tSTATE\tPID\tPROCESS\tCONTAINER\tLOCAL\tREMOTE\tDIR")
	for _, r := range records {
		fmt.Fprintf(w, "%s\t%d\t%s\t%s\t%d\t%s\t%s\t%s\t%s\t%s\n",
			r.LastSeen.Format(time.RFC3339),
			r.SeenCount,
			r.Proto,
			r.State,
			r.PID,
			emptyDash(r.Process),
			shortContainer(r.Container),
			fmt.Sprintf("%s:%d", r.LocalIP, r.LocalPort),
			fmt.Sprintf("%s:%d", r.RemoteIP, r.RemotePort),
			r.Direction,
		)
	}
	w.Flush()
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func emptyDash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

func shortContainer(value string) string {
	if value == "" {
		return "-"
	}
	if len(value) > 12 {
		return value[:12]
	}
	return value
}
