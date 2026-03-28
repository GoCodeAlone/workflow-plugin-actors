package internal

import (
	"context"
	"fmt"

	goaktactor "github.com/tochemey/goakt/v4/actor"
)

// BridgeActor is a goakt Actor (PreStart/Receive/PostStop) used for permanent pools.
// It executes lightweight step pipelines when it receives messages.
// In the external plugin context, pipelines are restricted to built-in step types
// (step.set) that do not require a modular.Application.
type BridgeActor struct {
	poolName string
	identity string
	state    map[string]any
	handlers map[string]*HandlerPipeline
}

// newBridgeActor creates a BridgeActor ready to be spawned into a permanent pool.
func newBridgeActor(poolName, identity string, handlers map[string]*HandlerPipeline) *BridgeActor {
	return &BridgeActor{
		poolName: poolName,
		identity: identity,
		handlers: handlers,
	}
}

// PreStart initializes the actor.
func (a *BridgeActor) PreStart(_ *goaktactor.Context) error {
	if a.state == nil {
		a.state = make(map[string]any)
	}
	return nil
}

// PostStop cleans up the actor.
func (a *BridgeActor) PostStop(_ *goaktactor.Context) error {
	return nil
}

// Receive handles incoming messages by dispatching to the appropriate handler pipeline.
func (a *BridgeActor) Receive(ctx *goaktactor.ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *ActorMessage:
		result, err := executePipeline(ctx.Context(), msg, a.poolName, a.identity, a.state, a.handlers)
		if err != nil {
			ctx.Err(err)
			return
		}
		ctx.Response(result)
	default:
		// Ignore system messages (PostStart, PoisonPill, etc.)
		_ = msg
	}
}

// BridgeGrain is a goakt Grain (OnActivate/OnReceive/OnDeactivate) used for auto-managed pools.
// Grains are virtual actors: activated on first message, passivated after idleTimeout.
type BridgeGrain struct {
	poolName string
	state    map[string]any
	handlers map[string]*HandlerPipeline
}

// OnActivate initializes grain state when the grain is loaded into memory.
func (g *BridgeGrain) OnActivate(_ context.Context, _ *goaktactor.GrainProps) error {
	if g.state == nil {
		g.state = make(map[string]any)
	}
	return nil
}

// OnReceive dispatches an ActorMessage to the matching handler pipeline.
func (g *BridgeGrain) OnReceive(ctx *goaktactor.GrainContext) {
	msg, ok := ctx.Message().(*ActorMessage)
	if !ok {
		ctx.Unhandled()
		return
	}
	identity := ctx.Self().Name()
	result, err := executePipeline(ctx.Context(), msg, g.poolName, identity, g.state, g.handlers)
	if err != nil {
		ctx.Err(err)
		return
	}
	ctx.Response(result)
}

// OnDeactivate is called when the grain is passivated (idle timeout reached).
func (g *BridgeGrain) OnDeactivate(_ context.Context, _ *goaktactor.GrainProps) error {
	return nil
}

// executePipeline finds the handler for msg.Type, runs the step pipeline, updates state,
// and returns the accumulated output.
// Steps are executed inline: only step.set is natively supported; other step types
// are stored in handler config but not dispatched (they require the full engine).
func executePipeline(_ context.Context, msg *ActorMessage, poolName, identity string, state map[string]any, handlers map[string]*HandlerPipeline) (map[string]any, error) {
	handler, ok := handlers[msg.Type]
	if !ok {
		return nil, fmt.Errorf("no handler for message type %q", msg.Type)
	}

	// Build a context map that mirrors workflow's PipelineContext
	current := copyMap(state)
	// Merge message payload into current
	for k, v := range msg.Payload {
		current[k] = v
	}

	triggerData := map[string]any{
		"message": map[string]any{
			"type":    msg.Type,
			"payload": msg.Payload,
		},
		"state": copyMap(state),
		"actor": map[string]any{
			"identity": identity,
			"pool":     poolName,
		},
	}
	_ = triggerData

	output := map[string]any{}
	stepOutputs := map[string]map[string]any{}

	for _, stepCfg := range handler.Steps {
		stepType, _ := stepCfg["type"].(string)
		stepName, _ := stepCfg["name"].(string)

		if stepType == "" || stepName == "" {
			return nil, fmt.Errorf("handler %q: step missing 'type' or 'name'", msg.Type)
		}

		stepResult, stop, err := executeStep(stepType, stepName, stepCfg, current, stepOutputs)
		if err != nil {
			return nil, fmt.Errorf("handler %q step %q failed: %w", msg.Type, stepName, err)
		}

		if stepResult != nil {
			stepOutputs[stepName] = stepResult
			for k, v := range stepResult {
				output[k] = v
				current[k] = v
			}
		}

		if stop {
			break
		}
	}

	// Merge accumulated output into actor state
	for k, v := range output {
		state[k] = v
	}

	return output, nil
}

// executeStep executes a single step. Only step.set is natively supported in
// the external plugin. All other step types are ignored (no-op) and logged.
// Returns (output, stopPipeline, error).
func executeStep(stepType, _ string, stepCfg map[string]any, current map[string]any, stepOutputs map[string]map[string]any) (map[string]any, bool, error) {
	_ = stepOutputs
	switch stepType {
	case "step.set":
		return executeSetStep(stepCfg, current)
	default:
		// Unknown step types are silently no-ops in the external plugin context.
		// For full step support, use the workflow engine's native actors plugin.
		return nil, false, nil
	}
}

// executeSetStep implements step.set: merges values map into current context.
func executeSetStep(stepCfg map[string]any, current map[string]any) (map[string]any, bool, error) {
	config, _ := stepCfg["config"].(map[string]any)
	values, ok := config["values"].(map[string]any)
	if !ok {
		return map[string]any{}, false, nil
	}

	out := make(map[string]any, len(values))
	for k, v := range values {
		// Simple template resolution: replace {{.field}} references from current context
		if s, ok := v.(string); ok {
			out[k] = resolveSimpleTemplate(s, current)
		} else {
			out[k] = v
		}
	}
	return out, false, nil
}

// resolveSimpleTemplate does minimal template resolution for step.set values.
// It supports simple {{.field}} references from the current context map.
// Full template engine is not available in the external plugin.
func resolveSimpleTemplate(tmpl string, current map[string]any) any {
	// If the template doesn't look like a template expression, return as-is
	if len(tmpl) < 4 || tmpl[:2] != "{{" {
		return tmpl
	}
	// Parse simple "{{ .field }}" patterns
	inner := tmpl[2 : len(tmpl)-2]
	// Trim spaces
	for len(inner) > 0 && (inner[0] == ' ' || inner[0] == '\t') {
		inner = inner[1:]
	}
	for len(inner) > 0 && (inner[len(inner)-1] == ' ' || inner[len(inner)-1] == '\t') {
		inner = inner[:len(inner)-1]
	}
	if len(inner) > 0 && inner[0] == '.' {
		field := inner[1:]
		if v, ok := current[field]; ok {
			return v
		}
	}
	return tmpl
}

// copyMap creates a shallow copy of a map.
func copyMap(m map[string]any) map[string]any {
	cp := make(map[string]any, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return cp
}
