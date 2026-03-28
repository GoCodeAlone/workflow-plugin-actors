// Command workflow-plugin-actors is a workflow engine external plugin that
// provides actor model support (goakt v4): stateful entities, fault-tolerant
// message-driven workflows.
// It runs as a subprocess and communicates with the host workflow engine via
// the go-plugin gRPC protocol.
package main

import (
	"github.com/GoCodeAlone/workflow-plugin-actors/internal"
	sdk "github.com/GoCodeAlone/workflow/plugin/external/sdk"
)

func main() {
	sdk.Serve(internal.NewActorsPlugin())
}
