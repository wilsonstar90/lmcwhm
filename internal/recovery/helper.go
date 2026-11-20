package recovery

import "context"

type HelperClient interface {
	Call(ctx context.Context, action, name string) error
}

type HelperRunner struct {
	Runner Runner
	Client HelperClient
}

func (r HelperRunner) Targets(ctx context.Context) ([]string, error) { return r.Runner.Targets(ctx) }
func (r HelperRunner) Inspect(ctx context.Context, name string) (State, error) {
	return r.Runner.Inspect(ctx, name)
}
func (r HelperRunner) Ready(ctx context.Context, name string) error { return r.Runner.Ready(ctx, name) }
func (r HelperRunner) Restart(ctx context.Context, name string) error {
	return r.Client.Call(ctx, "docker.restart", name)
}
