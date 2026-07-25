package paths

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
)

type Paths struct {
	ConfigDir   string
	ConfigFile  string
	DataDir     string
	StateDir    string
	SessionsDir string
	ServerFile  string
	LockFile    string
}

func Resolve() (Paths, error) {
	if home := os.Getenv("AGENTHUB_HOME"); home != "" {
		root, err := filepath.Abs(home)
		if err != nil {
			return Paths{}, err
		}
		return fromRoots(
			filepath.Join(root, "config"),
			filepath.Join(root, "data"),
			filepath.Join(root, "state"),
		), nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, err
	}
	configRoot := filepath.Join(home, ".agenthub")

	var dataRoot, stateRoot string
	switch runtime.GOOS {
	case "darwin":
		dataRoot = filepath.Join(home, "Library", "Application Support")
		stateRoot = filepath.Join(home, "Library", "Application Support")
	case "windows":
		dataRoot = os.Getenv("LOCALAPPDATA")
		stateRoot = dataRoot
		if dataRoot == "" {
			return Paths{}, errors.New("LOCALAPPDATA is not set")
		}
	default:
		dataRoot = os.Getenv("XDG_DATA_HOME")
		if dataRoot == "" {
			dataRoot = filepath.Join(home, ".local", "share")
		}
		stateRoot = os.Getenv("XDG_STATE_HOME")
		if stateRoot == "" {
			stateRoot = filepath.Join(home, ".local", "state")
		}
	}
	return fromRoots(
		configRoot,
		filepath.Join(dataRoot, "agenthub"),
		filepath.Join(stateRoot, "agenthub"),
	), nil
}

func fromRoots(configDir, dataDir, stateDir string) Paths {
	return Paths{
		ConfigDir:   configDir,
		ConfigFile:  filepath.Join(configDir, "config.json"),
		DataDir:     dataDir,
		StateDir:    stateDir,
		SessionsDir: filepath.Join(dataDir, "sessions"),
		ServerFile:  filepath.Join(stateDir, "server.json"),
		LockFile:    filepath.Join(stateDir, "server.lock"),
	}
}

func (p Paths) Ensure() error {
	for _, dir := range []string{p.ConfigDir, p.DataDir, p.StateDir, p.SessionsDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	return nil
}
