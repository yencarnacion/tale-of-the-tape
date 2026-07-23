package config

import "testing"

func TestDefaultPort(t *testing.T) {
	if Defaults().App.Addr != "127.0.0.1:3000" {
		t.Fatalf("default addr=%q", Defaults().App.Addr)
	}
}
