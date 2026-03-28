package internal

import (
	"context"
	"fmt"
	"time"

	sdk "github.com/GoCodeAlone/workflow/plugin/external/sdk"
	"github.com/tochemey/goakt/v4/actor"
)

// actorSendStep implements sdk.StepInstance for step.actor_send.
// It sends a fire-and-forget message to an actor (Tell).
type actorSendStep struct {
	name     string
	pool     string
	identity string
	message  map[string]any
}

// newActorSendStep creates an actorSendStep from config.
func newActorSendStep(name string, config map[string]any) (*actorSendStep, error) {
	pool, _ := config["pool"].(string)
	if pool == "" {
		return nil, fmt.Errorf("step.actor_send %q: 'pool' is required", name)
	}

	message, ok := config["message"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("step.actor_send %q: 'message' map is required", name)
	}

	msgType, _ := message["type"].(string)
	if msgType == "" {
		return nil, fmt.Errorf("step.actor_send %q: 'message.type' is required", name)
	}

	identity, _ := config["identity"].(string)

	return &actorSendStep{
		name:     name,
		pool:     pool,
		identity: identity,
		message:  message,
	}, nil
}

// Execute sends a fire-and-forget message to an actor pool.
func (s *actorSendStep) Execute(ctx context.Context, triggerData map[string]any, stepOutputs map[string]map[string]any, current map[string]any, _ map[string]any, _ map[string]any) (*sdk.StepResult, error) {
	// Build a combined context for template resolution
	resolveCtx := buildResolveCtx(triggerData, stepOutputs, current)

	// Resolve message fields
	resolved := resolveMap(s.message, resolveCtx)
	msgType, _ := resolved["type"].(string)
	payload, _ := resolved["payload"].(map[string]any)
	if payload == nil {
		payload = map[string]any{}
	}

	// Resolve identity
	identity := s.identity
	if identity != "" {
		identity = resolveString(identity, resolveCtx)
	}

	pool, err := poolRegistry.get(s.pool)
	if err != nil {
		return nil, fmt.Errorf("step.actor_send %q: %w", s.name, err)
	}

	if pool.system == nil || pool.system.ActorSystem() == nil {
		return nil, fmt.Errorf("step.actor_send %q: actor system not started", s.name)
	}
	sys := pool.system.ActorSystem()

	msg := &ActorMessage{Type: msgType, Payload: payload}

	if pool.Mode() == "auto-managed" && identity == "" {
		return nil, fmt.Errorf("step.actor_send %q: 'identity' is required for auto-managed pool %q", s.name, s.pool)
	}

	if pool.Mode() == "auto-managed" && identity != "" {
		grainID, err := pool.GetGrainIdentity(ctx, identity)
		if err != nil {
			return nil, fmt.Errorf("step.actor_send %q: failed to get grain %q: %w", s.name, identity, err)
		}
		if err := sys.TellGrain(ctx, grainID, msg); err != nil {
			return nil, fmt.Errorf("step.actor_send %q: tell failed: %w", s.name, err)
		}
	} else {
		pids, err := pool.SelectActor(msg)
		if err != nil {
			return nil, fmt.Errorf("step.actor_send %q: %w", s.name, err)
		}
		for _, pid := range pids {
			if err := actor.Tell(ctx, pid, msg); err != nil {
				return nil, fmt.Errorf("step.actor_send %q: tell failed: %w", s.name, err)
			}
		}
	}

	return &sdk.StepResult{
		Output: map[string]any{"delivered": true},
	}, nil
}

// actorAskStep implements sdk.StepInstance for step.actor_ask.
// It sends a request-response message to an actor (Ask).
type actorAskStep struct {
	name     string
	pool     string
	identity string
	timeout  time.Duration
	message  map[string]any
}

// newActorAskStep creates an actorAskStep from config.
func newActorAskStep(name string, config map[string]any) (*actorAskStep, error) {
	pool, _ := config["pool"].(string)
	if pool == "" {
		return nil, fmt.Errorf("step.actor_ask %q: 'pool' is required", name)
	}

	message, ok := config["message"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("step.actor_ask %q: 'message' map is required", name)
	}

	msgType, _ := message["type"].(string)
	if msgType == "" {
		return nil, fmt.Errorf("step.actor_ask %q: 'message.type' is required", name)
	}

	timeout := 10 * time.Second
	if v, ok := config["timeout"].(string); ok && v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("step.actor_ask %q: invalid timeout %q: %w", name, v, err)
		}
		timeout = d
	}

	identity, _ := config["identity"].(string)

	return &actorAskStep{
		name:     name,
		pool:     pool,
		identity: identity,
		timeout:  timeout,
		message:  message,
	}, nil
}

// Execute sends a request-response message to an actor and returns the response.
func (s *actorAskStep) Execute(ctx context.Context, triggerData map[string]any, stepOutputs map[string]map[string]any, current map[string]any, _ map[string]any, _ map[string]any) (*sdk.StepResult, error) {
	resolveCtx := buildResolveCtx(triggerData, stepOutputs, current)

	resolved := resolveMap(s.message, resolveCtx)
	msgType, _ := resolved["type"].(string)
	payload, _ := resolved["payload"].(map[string]any)
	if payload == nil {
		payload = map[string]any{}
	}

	identity := s.identity
	if identity != "" {
		identity = resolveString(identity, resolveCtx)
	}

	pool, err := poolRegistry.get(s.pool)
	if err != nil {
		return nil, fmt.Errorf("step.actor_ask %q: %w", s.name, err)
	}

	if pool.system == nil || pool.system.ActorSystem() == nil {
		return nil, fmt.Errorf("step.actor_ask %q: actor system not started", s.name)
	}
	sys := pool.system.ActorSystem()

	msg := &ActorMessage{Type: msgType, Payload: payload}
	var resp any

	if pool.Mode() == "auto-managed" && identity == "" {
		return nil, fmt.Errorf("step.actor_ask %q: 'identity' is required for auto-managed pool %q", s.name, s.pool)
	}

	if pool.Mode() == "permanent" && pool.Routing() == "broadcast" {
		return nil, fmt.Errorf("step.actor_ask %q: broadcast routing is not supported for ask (use step.actor_send instead)", s.name)
	}

	if pool.Mode() == "auto-managed" && identity != "" {
		grainID, err := pool.GetGrainIdentity(ctx, identity)
		if err != nil {
			return nil, fmt.Errorf("step.actor_ask %q: failed to get grain %q: %w", s.name, identity, err)
		}
		resp, err = sys.AskGrain(ctx, grainID, msg, s.timeout)
		if err != nil {
			return nil, fmt.Errorf("step.actor_ask %q: ask failed: %w", s.name, err)
		}
	} else {
		pids, err := pool.SelectActor(msg)
		if err != nil {
			return nil, fmt.Errorf("step.actor_ask %q: %w", s.name, err)
		}
		resp, err = actor.Ask(ctx, pids[0], msg, s.timeout)
		if err != nil {
			return nil, fmt.Errorf("step.actor_ask %q: ask failed: %w", s.name, err)
		}
	}

	output, ok := resp.(map[string]any)
	if !ok {
		output = map[string]any{"response": resp}
	}

	return &sdk.StepResult{Output: output}, nil
}

// buildResolveCtx builds a flat map for template resolution from pipeline context components.
func buildResolveCtx(triggerData map[string]any, stepOutputs map[string]map[string]any, current map[string]any) map[string]any {
	ctx := make(map[string]any)
	for k, v := range triggerData {
		ctx[k] = v
	}
	for k, v := range current {
		ctx[k] = v
	}
	if len(stepOutputs) > 0 {
		ctx["steps"] = stepOutputs
	}
	return ctx
}

// resolveMap does shallow template resolution on map values.
func resolveMap(m map[string]any, ctx map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		if s, ok := v.(string); ok {
			out[k] = resolveString(s, ctx)
		} else {
			out[k] = v
		}
	}
	return out
}

// resolveString does minimal Go template-style resolution for {{.field}} patterns.
func resolveString(s string, ctx map[string]any) string {
	if len(s) < 4 || s[:2] != "{{" {
		return s
	}
	inner := s[2 : len(s)-2]
	for len(inner) > 0 && (inner[0] == ' ' || inner[0] == '\t') {
		inner = inner[1:]
	}
	for len(inner) > 0 && (inner[len(inner)-1] == ' ' || inner[len(inner)-1] == '\t') {
		inner = inner[:len(inner)-1]
	}
	if len(inner) > 0 && inner[0] == '.' {
		field := inner[1:]
		if v, ok := ctx[field]; ok {
			return fmt.Sprintf("%v", v)
		}
	}
	return s
}
