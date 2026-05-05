package cli

import (
	"context"
	"io"
	"log/slog"
	"time"

	"github.com/ohanyere/cluster-meter/internal/kube"
	"github.com/spf13/cobra"
	"k8s.io/client-go/kubernetes"
)

type RootOptions struct {
	Version       string
	Stdout        io.Writer
	Stderr        io.Writer
	Logger        *slog.Logger
	ClientFactory func(context.Context, string) (kubernetes.Interface, error)
}

type dependencies struct {
	stdout        io.Writer
	stderr        io.Writer
	logger        *slog.Logger
	clientFactory func(context.Context, string) (kubernetes.Interface, error)
}

type flagValues struct {
	kubeconfig        string
	noColor           bool
	ai                bool
	aiCacheTTL        time.Duration
	noAICache         bool
	aiTimeout         time.Duration
	stateFile         string
	noState           bool
	watch             bool
	alert             bool
	interval          time.Duration
	alertRepeat       time.Duration
	criticalThreshold float64
}

func NewRootCommand(opts RootOptions) *cobra.Command {
	stdout := opts.Stdout
	if stdout == nil {
		stdout = io.Discard
	}

	stderr := opts.Stderr
	if stderr == nil {
		stderr = io.Discard
	}

	logger := opts.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	deps := dependencies{
		stdout:        stdout,
		stderr:        stderr,
		logger:        logger,
		clientFactory: opts.ClientFactory,
	}
	if deps.clientFactory == nil {
		deps.clientFactory = func(_ context.Context, kubeconfigPath string) (kubernetes.Interface, error) {
			return kube.NewClientset(kubeconfigPath)
		}
	}

	flags := &flagValues{}

	cmd := &cobra.Command{
		Use:           "cluster-meter",
		Short:         "Inspect foundational Kubernetes cluster capacity signals",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.PersistentFlags().StringVar(&flags.kubeconfig, "kubeconfig", "", "Path to the kubeconfig file")
	cmd.PersistentFlags().BoolVar(&flags.noColor, "no-color", false, "Disable ANSI color output")

	cmd.AddCommand(newVersionCommand(opts.Version, deps))
	cmd.AddCommand(newCapacityCommand(flags, deps))
	cmd.AddCommand(newServeCommand(flags, deps))

	return cmd
}
