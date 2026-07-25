package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type State struct {
	PID       int       `json:"pid"`
	Endpoint  string    `json:"endpoint"`
	StartedAt time.Time `json:"startedAt"`
}

type Lock struct {
	path string
	pid  int
}

func AcquireLock(path string) (*Lock, error) {
	pid := os.Getpid()
	for attempt := 0; attempt < 2; attempt++ {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			if _, err := fmt.Fprintf(file, "%d\n", pid); err != nil {
				_ = file.Close()
				_ = os.Remove(path)
				return nil, err
			}
			if err := file.Sync(); err != nil {
				_ = file.Close()
				_ = os.Remove(path)
				return nil, err
			}
			if err := file.Close(); err != nil {
				_ = os.Remove(path)
				return nil, err
			}
			return &Lock{path: path, pid: pid}, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		existing, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil, readErr
		}
		existingPID, parseErr := strconv.Atoi(strings.TrimSpace(string(existing)))
		if parseErr == nil && processAlive(existingPID) {
			return nil, fmt.Errorf("agenthub is already running with pid %d", existingPID)
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	return nil, errors.New("could not acquire daemon lock")
}

func (l *Lock) Release() error {
	data, err := os.ReadFile(l.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if strings.TrimSpace(string(data)) != strconv.Itoa(l.pid) {
		return errors.New("daemon lock ownership changed")
	}
	return os.Remove(l.path)
}

func WriteState(path string, state State) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	temp, err := os.CreateTemp(filepath.Dir(path), ".server-*.json")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	cleanup := func() {
		_ = temp.Close()
		_ = os.Remove(tempPath)
	}
	if err := temp.Chmod(0o600); err != nil {
		cleanup()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		cleanup()
		return err
	}
	if err := temp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := temp.Close(); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	return nil
}

func ReadState(path string) (State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return State{}, err
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return State{}, err
	}
	if state.Endpoint == "" {
		return State{}, errors.New("server state has no endpoint")
	}
	return state, nil
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}
