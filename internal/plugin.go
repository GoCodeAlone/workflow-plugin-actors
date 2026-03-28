package internal

import (
	"fmt"

	sdk "github.com/GoCodeAlone/workflow/plugin/external/sdk"
)

// actorsPlugin implements sdk.PluginProvider, sdk.ModuleProvider, and sdk.StepProvider.
type actorsPlugin struct{}

// NewActorsPlugin returns a new actorsPlugin instance.
func NewActorsPlugin() sdk.PluginProvider {
	return &actorsPlugin{}
}

// Manifest returns plugin metadata.
func (p *actorsPlugin) Manifest() sdk.PluginManifest {
	return sdk.PluginManifest{
		Name:        "workflow-plugin-actors",
		Version:     "0.1.0",
		Author:      "GoCodeAlone",
		Description: "Actor model support with goakt v4 — stateful entities, fault-tolerant message-driven workflows",
	}
}

// ModuleTypes returns the module type names this plugin provides.
func (p *actorsPlugin) ModuleTypes() []string {
	return []string{
		"actor.system",
		"actor.pool",
	}
}

// CreateModule creates a module instance of the given type.
func (p *actorsPlugin) CreateModule(typeName, name string, config map[string]any) (sdk.ModuleInstance, error) {
	switch typeName {
	case "actor.system":
		return newActorSystemModule(name, config)
	case "actor.pool":
		return newActorPoolModule(name, config)
	default:
		return nil, fmt.Errorf("actors plugin: unknown module type %q", typeName)
	}
}

// StepTypes returns the step type names this plugin provides.
func (p *actorsPlugin) StepTypes() []string {
	return []string{
		"step.actor_send",
		"step.actor_ask",
	}
}

// CreateStep creates a step instance of the given type.
func (p *actorsPlugin) CreateStep(typeName, name string, config map[string]any) (sdk.StepInstance, error) {
	switch typeName {
	case "step.actor_send":
		return newActorSendStep(name, config)
	case "step.actor_ask":
		return newActorAskStep(name, config)
	default:
		return nil, fmt.Errorf("actors plugin: unknown step type %q", typeName)
	}
}
