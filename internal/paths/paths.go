package paths

import (
	"os"
	"path/filepath"
)

// Paths is the resolved set of files and directories AgentHub reads and
// writes. With the default layout everything lives under a single data
// root, $HOME/.agenthub:
//
//	~/.agenthub/
//	├── config.json
//	├── sessions/<id>/         (active sessions)
//	├── sessions/Archive/<id>/ (archived sessions)
//	├── logs/                  (daemon stdout/stderr when launched as a service)
//	├── server.json            (transient endpoint discovery)
//	└── server.lock            (transient single-daemon lock)
type Paths struct {
	ConfigDir   string
	ConfigFile  string
	DataDir     string
	StateDir    string
	SessionsDir string
	LogsDir     string
	ServerFile  string
	LockFile    string
}

// RootName is the name of the default data root inside the user's home.
const RootName = ".agenthub"

func Resolve() (Paths, error) {
	if home := os.Getenv("AGENTHUB_HOME"); home != "" {
		root, err := filepath.Abs(home)
		if err != nil {
			return Paths{}, err
		}
		paths := fromRoots(
			filepath.Join(root, "config"),
			filepath.Join(root, "data"),
			filepath.Join(root, "state"),
		)
		paths.LogsDir = filepath.Join(root, "logs")
		return paths, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, err
	}
	return Default(home), nil
}

// Default returns the default layout rooted at home: every AgentHub file
// lives directly under home/.agenthub.
func Default(home string) Paths {
	root := filepath.Join(home, RootName)
	paths := fromRoots(root, root, root)
	paths.LogsDir = filepath.Join(root, "logs")
	return paths
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
	for _, dir := range []string{p.ConfigDir, p.DataDir, p.StateDir, p.SessionsDir, p.LogsDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	return nil
}
