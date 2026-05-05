package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const kubeconfigEnvKey = "KUBECONFIG"

type Config struct {
	KubeconfigPath string
}

type Environment interface {
	Getenv(string) string
	UserHomeDir() (string, error)
}

type LoadOptions struct {
	ExplicitKubeconfig string
	Environment        Environment
}

type systemEnv struct{}

func SystemEnv() Environment {
	return systemEnv{}
}

func (systemEnv) Getenv(key string) string {
	return os.Getenv(key)
}

func (systemEnv) UserHomeDir() (string, error) {
	return os.UserHomeDir()
}

func Load(opts LoadOptions) (Config, error) {
	env := opts.Environment
	if env == nil {
		env = SystemEnv()
	}

	path, err := resolveKubeconfigPath(opts.ExplicitKubeconfig, env)
	if err != nil {
		return Config{}, err
	}

	return Config{KubeconfigPath: path}, nil
}

func resolveKubeconfigPath(explicit string, env Environment) (string, error) {
	if explicit != "" {
		return explicit, nil
	}

	if envValue := strings.TrimSpace(env.Getenv(kubeconfigEnvKey)); envValue != "" {
		parts := filepath.SplitList(envValue)
		if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
			return "", fmt.Errorf("%s is set but does not contain a valid path", kubeconfigEnvKey)
		}

		return parts[0], nil
	}

	homeDir, err := env.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve kubeconfig home directory: %w", err)
	}

	return filepath.Join(homeDir, ".kube", "config"), nil
}
