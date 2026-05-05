package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/ohanyere/cluster-meter/internal/ai"
	"github.com/ohanyere/cluster-meter/internal/api"
	"github.com/ohanyere/cluster-meter/internal/config"
	statepkg "github.com/ohanyere/cluster-meter/internal/state"
	"github.com/spf13/cobra"
)

func newServeCommand(flags *flagValues, deps dependencies) *cobra.Command {
	var port int

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the cluster-meter HTTP API server",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			cfg, err := config.Load(config.LoadOptions{
				ExplicitKubeconfig: flags.kubeconfig,
				Environment:        config.SystemEnv(),
			})
			if err != nil {
				return fmt.Errorf("load kubeconfig: %w", err)
			}

			clientset, err := deps.clientFactory(ctx, cfg.KubeconfigPath)
			if err != nil {
				return fmt.Errorf("create kubernetes client: %w", err)
			}

			stateFile := flags.stateFile
			if !flags.noState && stateFile == "" {
				stateFile, err = apiStatePath()
				if err != nil {
					return err
				}
			}

			apiServer, err := api.NewServer(api.Options{
				Client:      clientset,
				StateFile:   stateFile,
				NoState:     flags.noState,
				AICacheTTL:  ai.DefaultCacheTTL,
				AITimeout:   10 * time.Second,
				AICacheFile: "",
			})
			if err != nil {
				return err
			}

			httpServer := &http.Server{
				Addr:              ":" + strconv.Itoa(port),
				Handler:           apiServer.Router(),
				ReadHeaderTimeout: 5 * time.Second,
				ReadTimeout:       10 * time.Second,
				WriteTimeout:      15 * time.Second,
				IdleTimeout:       60 * time.Second,
			}

			deps.logger.Info("API server running on :" + strconv.Itoa(port))

			errCh := make(chan error, 1)
			go func() {
				errCh <- httpServer.ListenAndServe()
			}()

			select {
			case <-ctx.Done():
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := httpServer.Shutdown(shutdownCtx); err != nil {
					return err
				}
				return nil
			case err := <-errCh:
				if errors.Is(err, http.ErrServerClosed) {
					return nil
				}
				return err
			}
		},
	}

	cmd.Flags().IntVar(&port, "port", 8080, "Port for the HTTP API server")
	cmd.Flags().StringVar(&flags.stateFile, "state-file", "", "Path to the capacity state file")
	cmd.Flags().BoolVar(&flags.noState, "no-state", false, "Disable state persistence and change detection")

	return cmd
}

func apiStatePath() (string, error) {
	return statepkg.DefaultPath()
}
