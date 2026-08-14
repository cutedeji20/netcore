package main

import "testing"

func TestHealthURL(t *testing.T) {
	tests := []struct {
		name string
		addr string
		want string
	}{
		{name: "wildcard host", addr: ":8080", want: "http://127.0.0.1:8080/health/live"},
		{name: "explicit interface", addr: "127.0.0.1:9000", want: "http://127.0.0.1:9000/health/live"},
		{name: "all IPv4 interfaces", addr: "0.0.0.0:8080", want: "http://127.0.0.1:8080/health/live"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := healthURL(tt.addr)
			if err != nil {
				t.Fatalf("healthURL(%q): %v", tt.addr, err)
			}
			if got != tt.want {
				t.Fatalf("healthURL(%q) = %q, want %q", tt.addr, got, tt.want)
			}
		})
	}
}

func TestHealthURLRejectsAddressWithoutPort(t *testing.T) {
	if _, err := healthURL("localhost"); err == nil {
		t.Fatal("healthURL accepted an address without a port")
	}
}
