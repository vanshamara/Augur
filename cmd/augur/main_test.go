package main

import (
	"testing"

	"github.com/vanshamara/Augur/internal/core"
)

func TestParseBackends(t *testing.T) {
	specs, err := parseBackends("fast=gpt-fast, stable=gpt-stable, gpt-direct")
	if err != nil {
		t.Fatalf("parse backends: %v", err)
	}
	want := []backendSpec{
		{ID: "fast", Model: "gpt-fast"},
		{ID: "stable", Model: "gpt-stable"},
		{ID: "gpt-direct", Model: "gpt-direct"},
	}
	if len(specs) != len(want) {
		t.Fatalf("got %d specs want %d", len(specs), len(want))
	}
	for i := range want {
		if specs[i] != want[i] {
			t.Fatalf("spec %d got %+v want %+v", i, specs[i], want[i])
		}
	}
}

func TestParseBackendsRequiresValue(t *testing.T) {
	if _, err := parseBackends(""); err == nil {
		t.Fatal("empty backend list should fail")
	}
}

func TestReadConfigUsesDefaults(t *testing.T) {
	config, err := readConfig(func(key string) string {
		if key == "AUGUR_BACKENDS" {
			return "a=model-a"
		}
		return ""
	})
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if config.Addr != defaultAddr {
		t.Fatalf("addr got %q", config.Addr)
	}
	if config.Backends[0].ID != core.BackendID("a") || config.Backends[0].Model != "model-a" {
		t.Fatalf("backend got %+v", config.Backends[0])
	}
}
