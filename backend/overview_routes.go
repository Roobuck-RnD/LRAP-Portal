package main

import (
	"bufio"
	"encoding/json"
	"net/http"
	"os/exec"
	"strings"
)

// ---------- 数据结构 ----------

type ArpEntry struct {
	IP  string `json:"ip"`
	Mac string `json:"mac"`
	Dev string `json:"dev"`
}

type RouteEntry struct {
	Network string `json:"network"`
	Target  string `json:"target"`  // Interface device, e.g. br-lan / eth1
	Gateway string `json:"gateway"` // "-" if no gateway
	Metric  string `json:"metric"`
	Table   string `json:"table"`
}

// ---------- Parsers: AC local commands ----------

func parseArpOutput(raw string) []ArpEntry {
	list := make([]ArpEntry, 0)

	scanner := bufio.NewScanner(strings.NewReader(raw))

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		parts := strings.Fields(line)
		if len(parts) < 3 {
			continue
		}

		entry := ArpEntry{
			IP: parts[0],
		}

		state := parts[len(parts)-1]

		for i := 1; i < len(parts); i++ {
			if parts[i] == "dev" && i+1 < len(parts) {
				entry.Dev = parts[i+1]
			}

			if parts[i] == "lladdr" && i+1 < len(parts) {
				entry.Mac = strings.ToUpper(parts[i+1])
			}
		}

		if entry.IP == "" || entry.Mac == "" {
			continue
		}

		if state == "FAILED" || state == "INCOMPLETE" {
			continue
		}

		if entry.Mac == "00:00:00:00:00:00" {
			continue
		}

		list = append(list, entry)
	}

	return list
}

func parseRouteOutput(raw string) []RouteEntry {
	list := make([]RouteEntry, 0)

	scanner := bufio.NewScanner(strings.NewReader(raw))

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		parts := strings.Fields(line)
		if len(parts) == 0 {
			continue
		}

		entry := RouteEntry{
			Network: parts[0],
			Table:   "main",
			Metric:  "0",
			Gateway: "-",
		}

		if entry.Network == "default" {
			entry.Network = "0.0.0.0/0"
		}

		for i := 0; i < len(parts); i++ {
			switch parts[i] {
			case "via":
				if i+1 < len(parts) {
					entry.Gateway = parts[i+1]
				}

			case "dev":
				if i+1 < len(parts) {
					entry.Target = parts[i+1]
				}

			case "metric":
				if i+1 < len(parts) {
					entry.Metric = parts[i+1]
				}

			case "table":
				if i+1 < len(parts) {
					entry.Table = parts[i+1]
				}
			}
		}

		if entry.Network == "" || entry.Target == "" {
			continue
		}

		list = append(list, entry)
	}

	return list
}

// ---------- Local Readers ----------

func getLocalArpEntries() ([]ArpEntry, error) {
	cmd := exec.Command("ip", "-4", "neigh", "show")

	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, err
	}

	data := parseArpOutput(string(out))
	if data == nil {
		data = []ArpEntry{}
	}

	return data, nil
}

func getLocalRoutes() ([]RouteEntry, error) {
	// Only show main table.
	// This hides local kernel routes such as broadcast/local/lo entries.
	cmd := exec.Command("ip", "-4", "route", "show", "table", "main")

	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, err
	}

	data := parseRouteOutput(string(out))
	if data == nil {
		data = []RouteEntry{}
	}

	return data, nil
}

// ---------- Auth Helper ----------

func netStatusAuthorized(r *http.Request) bool {
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	return strings.HasPrefix(strings.ToLower(auth), "bearer ") && len(auth) > len("bearer ")
}

// ---------- Handlers: AC-only ----------

func arpHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	if !netStatusAuthorized(r) {
		http.Error(w, `{"error":"Unauthorized"}`, http.StatusUnauthorized)
		return
	}

	data, err := getLocalArpEntries()
	if err != nil {
		http.Error(w, `{"error":"failed to read AC ARP table"}`, http.StatusInternalServerError)
		return
	}

	_ = json.NewEncoder(w).Encode(data)
}

func routesHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	if !netStatusAuthorized(r) {
		http.Error(w, `{"error":"Unauthorized"}`, http.StatusUnauthorized)
		return
	}

	data, err := getLocalRoutes()
	if err != nil {
		http.Error(w, `{"error":"failed to read AC route table"}`, http.StatusInternalServerError)
		return
	}

	_ = json.NewEncoder(w).Encode(data)
}