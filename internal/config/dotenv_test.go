package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadEnvFilesUsesDotEnvWhenPresent(t *testing.T) {
	tempDir := t.TempDir()
	writeEnvFile(t, filepath.Join(tempDir, ".env"), "APP_ENV=prod\nAPP_PORT=3001\n")
	writeEnvFile(t, filepath.Join(tempDir, ".env.prod"), "APP_PORT=9999\nENV_SOURCE=prod\n")
	unsetEnv(t, "APP_ENV", "APP_PORT", "ENV_SOURCE")
	chdirForTest(t, tempDir)

	if err := LoadEnvFiles(); err != nil {
		t.Fatalf("LoadEnvFiles() error = %v", err)
	}

	if got := os.Getenv("APP_PORT"); got != "3001" {
		t.Fatalf("APP_PORT = %q, want 3001", got)
	}
	if got := os.Getenv("ENV_SOURCE"); got != "" {
		t.Fatalf("ENV_SOURCE = %q, want empty", got)
	}
}

func TestLoadEnvFilesFallsBackToDotEnvTestWhenDotEnvMissing(t *testing.T) {
	tempDir := t.TempDir()
	writeEnvFile(t, filepath.Join(tempDir, ".env.test"), "APP_PORT=3001\nENV_SOURCE=test\n")
	unsetEnv(t, "APP_ENV", "APP_PORT", "ENV_SOURCE")
	chdirForTest(t, tempDir)

	if err := LoadEnvFiles(); err != nil {
		t.Fatalf("LoadEnvFiles() error = %v", err)
	}

	if got := os.Getenv("APP_PORT"); got != "3001" {
		t.Fatalf("APP_PORT = %q, want 3001", got)
	}
	if got := os.Getenv("ENV_SOURCE"); got != "test" {
		t.Fatalf("ENV_SOURCE = %q, want test", got)
	}
}

func TestLoadEnvFilesFallsBackToDotEnvProdWhenDotEnvAndDotEnvTestMissing(t *testing.T) {
	tempDir := t.TempDir()
	writeEnvFile(t, filepath.Join(tempDir, ".env.prod"), "APP_PORT=3000\nENV_SOURCE=prod\n")
	unsetEnv(t, "APP_ENV", "APP_PORT", "ENV_SOURCE")
	chdirForTest(t, tempDir)

	if err := LoadEnvFiles(); err != nil {
		t.Fatalf("LoadEnvFiles() error = %v", err)
	}

	if got := os.Getenv("APP_PORT"); got != "3000" {
		t.Fatalf("APP_PORT = %q, want 3000", got)
	}
	if got := os.Getenv("ENV_SOURCE"); got != "prod" {
		t.Fatalf("ENV_SOURCE = %q, want prod", got)
	}
}

func writeEnvFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

func chdirForTest(t *testing.T, dir string) {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir() error = %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(cwd); err != nil {
			t.Fatalf("restore cwd error = %v", err)
		}
	})
}

func unsetEnv(t *testing.T, keys ...string) {
	t.Helper()
	type envState struct {
		key     string
		value   string
		existed bool
	}
	states := make([]envState, 0, len(keys))
	for _, key := range keys {
		value, existed := os.LookupEnv(key)
		states = append(states, envState{key: key, value: value, existed: existed})
		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("Unsetenv(%q) error = %v", key, err)
		}
	}
	t.Cleanup(func() {
		for _, state := range states {
			var err error
			if state.existed {
				err = os.Setenv(state.key, state.value)
			} else {
				err = os.Unsetenv(state.key)
			}
			if err != nil {
				t.Fatalf("restore env %q error = %v", state.key, err)
			}
		}
	})
}
