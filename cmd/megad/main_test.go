package main

import "testing"

func TestAllowedHostsIncludesListenerAndExplicitProxyName(t *testing.T) {
	hosts := allowedHosts("127.0.0.1:8080", "downloads.example.test")
	want := map[string]bool{
		"127.0.0.1:8080":         false,
		"localhost:8080":         false,
		"downloads.example.test": false,
	}
	for _, host := range hosts {
		if _, ok := want[host]; ok {
			want[host] = true
		}
	}
	for host, found := range want {
		if !found {
			t.Fatalf("allowed hosts omitted %q: %v", host, hosts)
		}
	}
}
