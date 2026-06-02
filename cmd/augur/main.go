package main

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/vanshamara/Augur/internal/backend"
	openaibackend "github.com/vanshamara/Augur/internal/backend/openai"
	"github.com/vanshamara/Augur/internal/core"
	"github.com/vanshamara/Augur/internal/dataplane"
	"github.com/vanshamara/Augur/internal/httpapi"
	"github.com/vanshamara/Augur/internal/openaiapi"
	"github.com/vanshamara/Augur/internal/router"
)

const defaultAddr = "127.0.0.1:8080"

type envConfig struct {
	Addr     string
	BaseURL  string
	Backends []backendSpec
}

type backendSpec struct {
	ID    core.BackendID
	Model string
}

func main() {
	config, err := readConfig(os.Getenv)
	if err != nil {
		log.Fatal(err)
	}

	client, err := openaiapi.New(openaiapi.Config{BaseURL: config.BaseURL})
	if err != nil {
		log.Fatal(err)
	}

	backends, err := buildBackends(config.Backends, client)
	if err != nil {
		log.Fatal(err)
	}
	gateway, err := dataplane.New(dataplane.Config{
		Router:   router.NewRoundRobin(),
		Backends: backends,
	})
	if err != nil {
		log.Fatal(err)
	}
	server, err := httpapi.New(httpapi.Config{Gateway: gateway})
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("augur listening on %s", config.Addr)
	log.Fatal(http.ListenAndServe(config.Addr, server))
}

func readConfig(getenv func(string) string) (envConfig, error) {
	addr := strings.TrimSpace(getenv("AUGUR_ADDR"))
	if addr == "" {
		addr = defaultAddr
	}
	backends, err := parseBackends(getenv("AUGUR_BACKENDS"))
	if err != nil {
		return envConfig{}, err
	}
	return envConfig{
		Addr:     addr,
		BaseURL:  strings.TrimSpace(getenv("AUGUR_OPENAI_BASE_URL")),
		Backends: backends,
	}, nil
}

func parseBackends(value string) ([]backendSpec, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, errors.New("AUGUR_BACKENDS is required, for example AUGUR_BACKENDS=fast=gpt-4o-mini,stable=gpt-4o")
	}

	parts := strings.Split(value, ",")
	backends := make([]backendSpec, 0, len(parts))
	for _, part := range parts {
		spec, err := parseBackendSpec(part)
		if err != nil {
			return nil, err
		}
		backends = append(backends, spec)
	}
	return backends, nil
}

func parseBackendSpec(value string) (backendSpec, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return backendSpec{}, errors.New("backend spec cannot be empty")
	}
	if !strings.Contains(value, "=") {
		return backendSpec{ID: core.BackendID(value), Model: value}, nil
	}

	parts := strings.SplitN(value, "=", 2)
	id := strings.TrimSpace(parts[0])
	model := strings.TrimSpace(parts[1])
	if id == "" || model == "" {
		return backendSpec{}, fmt.Errorf("invalid backend spec %q", value)
	}
	return backendSpec{ID: core.BackendID(id), Model: model}, nil
}

func buildBackends(specs []backendSpec, client *openaiapi.Client) ([]backend.Backend, error) {
	backends := make([]backend.Backend, 0, len(specs))
	for _, spec := range specs {
		b, err := openaibackend.New(openaibackend.Config{
			ID:     spec.ID,
			Model:  spec.Model,
			Client: client,
		})
		if err != nil {
			return nil, err
		}
		backends = append(backends, b)
	}
	return backends, nil
}
