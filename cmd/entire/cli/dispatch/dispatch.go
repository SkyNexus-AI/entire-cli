package dispatch

import "context"

type Mode int

const (
	ModeServer Mode = iota
	ModeLocal
)

type Options struct {
	Mode                  Mode
	RepoPaths             []string
	Org                   string
	Since                 string
	Until                 string
	Branches              []string
	AllBranches           bool
	ImplicitCurrentBranch bool
	Generate              bool
	Voice                 string
}

func Run(ctx context.Context, opts Options) (*Dispatch, error) {
	if opts.Mode == ModeServer {
		return runServer(ctx, opts)
	}
	return runLocal(ctx, opts)
}
