package internal

import (
	"context"
	"fmt"
	"hash/fnv"
	"math/rand"
	"sync/atomic"
	"time"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/supervisor"
)

// actorPoolModule defines a group of actors with shared behavior, routing, and recovery.
// It implements sdk.ModuleInstance.
type actorPoolModule struct {
	name       string
	systemName string
	mode       string // "auto-managed" or "permanent"

	// Auto-managed settings
	idleTimeout time.Duration

	// Permanent pool settings
	poolSize int
	pids     []*actor.PID // tracked PIDs for routing

	// Routing
	routing    string // "round-robin", "random", "broadcast", "sticky"
	routingKey string // required for sticky
	rrCounter  atomic.Uint64

	// Recovery
	recovery *supervisor.Supervisor

	// Resolved at Init
	system *actorSystemModule

	// Message handlers
	handlers map[string]*HandlerPipeline
}

// newActorPoolModule creates a new actor pool module from config.
func newActorPoolModule(name string, cfg map[string]any) (*actorPoolModule, error) {
	if name == "" {
		return nil, fmt.Errorf("actor.pool module requires a name")
	}

	systemName, _ := cfg["system"].(string)
	if systemName == "" {
		return nil, fmt.Errorf("actor.pool %q: 'system' is required (name of actor.system module)", name)
	}

	m := &actorPoolModule{
		name:        name,
		systemName:  systemName,
		mode:        "auto-managed",
		idleTimeout: 10 * time.Minute,
		poolSize:    10,
		routing:     "round-robin",
		handlers:    make(map[string]*HandlerPipeline),
	}

	// Parse mode
	if v, ok := cfg["mode"].(string); ok && v != "" {
		switch v {
		case "auto-managed", "permanent":
			m.mode = v
		default:
			return nil, fmt.Errorf("actor.pool %q: invalid mode %q (use 'auto-managed' or 'permanent')", name, v)
		}
	}

	// Parse idle timeout
	if v, ok := cfg["idleTimeout"].(string); ok && v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("actor.pool %q: invalid idleTimeout %q: %w", name, v, err)
		}
		m.idleTimeout = d
	}

	// Parse pool size
	if v, ok := cfg["poolSize"]; ok {
		switch val := v.(type) {
		case int:
			m.poolSize = val
		case float64:
			m.poolSize = int(val)
		}
	}

	// Parse routing
	if v, ok := cfg["routing"].(string); ok && v != "" {
		switch v {
		case "round-robin", "random", "broadcast", "sticky":
			m.routing = v
		default:
			return nil, fmt.Errorf("actor.pool %q: invalid routing %q (use 'round-robin', 'random', 'broadcast', or 'sticky')", name, v)
		}
	}

	// Parse routing key
	m.routingKey, _ = cfg["routingKey"].(string)
	if m.routing == "sticky" && m.routingKey == "" {
		return nil, fmt.Errorf("actor.pool %q: 'routingKey' is required when routing is 'sticky'", name)
	}

	// Parse recovery
	if recovery, ok := cfg["recovery"].(map[string]any); ok {
		sup, err := parseRecoveryConfig(recovery)
		if err != nil {
			return nil, fmt.Errorf("actor.pool %q: %w", name, err)
		}
		m.recovery = sup
	}

	return m, nil
}

// Init resolves the actor.system module reference via plugin-local registry
// and registers self for step.actor_send/ask to find.
func (m *actorPoolModule) Init() error {
	sys, err := systemRegistry.get(m.systemName)
	if err != nil {
		return fmt.Errorf("actor.pool %q: actor.system %q not found: %w", m.name, m.systemName, err)
	}
	m.system = sys

	// Register self in plugin-local pool registry
	poolRegistry.register(m.name, m)
	return nil
}

// Start spawns actors in the pool.
func (m *actorPoolModule) Start(ctx context.Context) error {
	if m.system == nil || m.system.ActorSystem() == nil {
		return fmt.Errorf("actor.pool %q: actor system not started", m.name)
	}

	if m.mode == "permanent" {
		sys := m.system.ActorSystem()
		m.pids = make([]*actor.PID, 0, m.poolSize)

		var spawnOpts []actor.SpawnOption
		if m.recovery != nil {
			spawnOpts = append(spawnOpts, actor.WithSupervisor(m.recovery))
		}

		for i := 0; i < m.poolSize; i++ {
			actorName := fmt.Sprintf("%s-%d", m.name, i)
			bridge := newBridgeActor(m.name, actorName, m.handlers)
			pid, err := sys.Spawn(ctx, actorName, bridge, spawnOpts...)
			if err != nil {
				return fmt.Errorf("actor.pool %q: failed to spawn actor %q: %w", m.name, actorName, err)
			}
			m.pids = append(m.pids, pid)
		}
	}

	return nil
}

// Stop is a no-op — actors are stopped when the ActorSystem shuts down.
func (m *actorPoolModule) Stop(_ context.Context) error {
	return nil
}

// SelectActor picks one or more PIDs from the permanent pool based on the routing strategy.
func (m *actorPoolModule) SelectActor(msg *ActorMessage) ([]*actor.PID, error) {
	if len(m.pids) == 0 {
		return nil, fmt.Errorf("actor.pool %q: no actors available", m.name)
	}

	switch m.routing {
	case "broadcast":
		return m.pids, nil

	case "random":
		idx := rand.Intn(len(m.pids)) //nolint:gosec
		return []*actor.PID{m.pids[idx]}, nil

	case "sticky":
		key := ""
		if msg != nil && msg.Payload != nil && m.routingKey != "" {
			if v, ok := msg.Payload[m.routingKey]; ok {
				key = fmt.Sprintf("%v", v)
			}
		}
		h := fnv.New32a()
		h.Write([]byte(key))
		idx := int(h.Sum32()) % len(m.pids)
		return []*actor.PID{m.pids[idx]}, nil

	default: // round-robin
		idx := m.rrCounter.Add(1) - 1
		return []*actor.PID{m.pids[idx%uint64(len(m.pids))]}, nil //nolint:gosec
	}
}

// GetGrainIdentity retrieves or activates a grain for the given identity.
func (m *actorPoolModule) GetGrainIdentity(ctx context.Context, identity string) (*actor.GrainIdentity, error) {
	if m.system == nil || m.system.ActorSystem() == nil {
		return nil, fmt.Errorf("actor.pool %q: actor system not started", m.name)
	}

	factory := func(_ context.Context) (actor.Grain, error) {
		return &BridgeGrain{
			poolName: m.name,
			handlers: m.handlers,
		}, nil
	}

	return m.system.ActorSystem().GrainIdentity(ctx, identity, factory,
		actor.WithGrainDeactivateAfter(m.idleTimeout),
	)
}

// SetHandlers sets the message receive handlers.
func (m *actorPoolModule) SetHandlers(handlers map[string]*HandlerPipeline) {
	m.handlers = handlers
}

// Mode returns the lifecycle mode.
func (m *actorPoolModule) Mode() string { return m.mode }

// Routing returns the routing strategy.
func (m *actorPoolModule) Routing() string { return m.routing }
