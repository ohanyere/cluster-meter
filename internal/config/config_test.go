package config

import (
	"errors"
	"testing"
)

func TestLoadPrefersExplicitKubeconfig(t *testing.T) {
	t.Parallel()

	cfg, err := Load(LoadOptions{
		ExplicitKubeconfig: "/tmp/custom-config",
		Environment: fakeEnvironment{
			env: map[string]string{
				kubeconfigEnvKey: "/tmp/env-config",
			},
			homeDir: "/home/tester",
		},
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.KubeconfigPath != "/tmp/custom-config" {
		t.Fatalf("expected explicit kubeconfig, got %q", cfg.KubeconfigPath)
	}
}

func TestLoadUsesEnvKubeconfigBeforeDefault(t *testing.T) {
	t.Parallel()

	cfg, err := Load(LoadOptions{
		Environment: fakeEnvironment{
			env: map[string]string{
				kubeconfigEnvKey: "/tmp/env-config:/tmp/secondary",
			},
			homeDir: "/home/tester",
		},
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.KubeconfigPath != "/tmp/env-config" {
		t.Fatalf("expected first kubeconfig entry, got %q", cfg.KubeconfigPath)
	}
}

func TestLoadFallsBackToDefaultHomeDir(t *testing.T) {
	t.Parallel()

	cfg, err := Load(LoadOptions{
		Environment: fakeEnvironment{
			homeDir: "/home/tester",
		},
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.KubeconfigPath != "/home/tester/.kube/config" {
		t.Fatalf("unexpected default kubeconfig path %q", cfg.KubeconfigPath)
	}
}

func TestLoadReturnsHomeDirError(t *testing.T) {
	t.Parallel()

	_, err := Load(LoadOptions{
		Environment: fakeEnvironment{
			homeErr: errors.New("boom"),
		},
	})
	if err == nil {
		t.Fatal("expected error but got nil")
	}
}

type fakeEnvironment struct {
	env     map[string]string
	homeDir string
	homeErr error
}

func (f fakeEnvironment) Getenv(key string) string {
	return f.env[key]
}

func (f fakeEnvironment) UserHomeDir() (string, error) {
	if f.homeErr != nil {
		return "", f.homeErr
	}

	return f.homeDir, nil
}
