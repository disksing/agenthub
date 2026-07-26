package paths

import (
	"os"
	"path/filepath"
	"runtime"
)

// Paths is the resolved set of files and directories AgentHub reads and
// writes. With the default layout everything lives under a single data
// root, $HOME/.agenthub:
//
//	~/.agenthub/
//	├── config.json            (plus legacy-agent-names.json when migrated)
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
	// Isolated reports that AGENTHUB_HOME redirected every path into an
	// explicit directory chosen by the user. Isolated layouts keep their
	// historical config/data/state subdirectories and are never touched by
	// the default-layout migration.
	Isolated bool
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
		paths.Isolated = true
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

// LegacyPaths locates the directories older AgentHub versions used by
// default, before everything moved under ~/.agenthub. Empty fields mean the
// platform had no such default; migration only ever reads from these
// locations and only when the current layout is the non-isolated default.
type LegacyPaths struct {
	// DataDir held sessions/Archive plus the transient server.json and
	// server.lock (macOS: ~/Library/Application Support/agenthub).
	DataDir string
	// LogsDir held the service stdout/stderr logs written by the launchd
	// plist (macOS only: ~/Library/Logs/AgentHub).
	LogsDir string
}

// LegacyDefaults returns the pre-unification default locations for the
// current platform and user. It never honors AGENTHUB_HOME: an explicit
// isolated layout means there is nothing legacy to migrate.
func LegacyDefaults() (LegacyPaths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return LegacyPaths{}, err
	}
	return LegacyFor(home, runtime.GOOS, os.Getenv), nil
}

// LegacyFor computes legacy default locations from explicit inputs so tests
// can exercise every platform without environment manipulation.
func LegacyFor(home, goos string, getenv func(string) string) LegacyPaths {
	switch goos {
	case "darwin":
		return LegacyPaths{
			DataDir: filepath.Join(home, "Library", "Application Support", "agenthub"),
			LogsDir: filepath.Join(home, "Library", "Logs", "AgentHub"),
		}
	case "windows":
		dataRoot := getenv("LOCALAPPDATA")
		if dataRoot == "" {
			return LegacyPaths{}
		}
		return LegacyPaths{DataDir: filepath.Join(dataRoot, "agenthub")}
	default:
		dataRoot := getenv("XDG_DATA_HOME")
		if dataRoot == "" {
			dataRoot = filepath.Join(home, ".local", "share")
		}
		return LegacyPaths{DataDir: filepath.Join(dataRoot, "agenthub")}
	}
}
