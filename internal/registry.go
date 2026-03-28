// Package internal implements the workflow-plugin-actors plugin.
// It provides actor model support via goakt v4 — actor.system and actor.pool
// modules, plus step.actor_send and step.actor_ask steps.
//
// Because this is an external (gRPC) plugin, modules and steps cannot share a
// modular.Application service registry. Instead a plugin-local pool registry
// is used so that step.actor_send / step.actor_ask can locate actor pools by
// name within the same plugin process.
package internal

import (
	"fmt"
	"sync"
)

// poolRegistry is a process-local map from pool name → ActorPoolModule.
// All module and step instances within the same plugin process share it.
var poolRegistry = &registry{}

type registry struct {
	mu    sync.RWMutex
	pools map[string]*actorPoolModule
}

func (r *registry) register(name string, p *actorPoolModule) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.pools == nil {
		r.pools = make(map[string]*actorPoolModule)
	}
	r.pools[name] = p
}

func (r *registry) get(name string) (*actorPoolModule, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.pools[name]
	if !ok {
		return nil, fmt.Errorf("actor pool %q not found", name)
	}
	return p, nil
}

// systemRegistry is a process-local map from system name → actorSystemModule.
var systemRegistry = &sysRegistry{}

type sysRegistry struct {
	mu      sync.RWMutex
	systems map[string]*actorSystemModule
}

func (r *sysRegistry) register(name string, s *actorSystemModule) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.systems == nil {
		r.systems = make(map[string]*actorSystemModule)
	}
	r.systems[name] = s
}

func (r *sysRegistry) get(name string) (*actorSystemModule, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.systems[name]
	if !ok {
		return nil, fmt.Errorf("actor system %q not found", name)
	}
	return s, nil
}
