package analyzer

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// StateFile 把分析仪状态（校准证档案、窗口、全部历史原始响应）持久化到磁盘。
// 校准证与原始响应是长期质量记录，进程重启后必须完整恢复。
type StateFile struct{ path string }

func NewStateFile(path string) *StateFile { return &StateFile{path: path} }

func (f *StateFile) Path() string { return f.path }

func (f *StateFile) Save(state *State) error {
	if err := os.MkdirAll(filepath.Dir(f.path), 0755); err != nil {
		return err
	}
	data, err := json.Marshal(Capture(state))
	if err != nil {
		return err
	}
	temporary := f.path + ".tmp"
	if err := os.WriteFile(temporary, data, 0644); err != nil {
		return err
	}
	if err := os.Rename(temporary, f.path); err != nil {
		return err
	}
	return nil
}

func (f *StateFile) Load() (RecoveryState, error) {
	data, err := os.ReadFile(f.path)
	if err != nil {
		return RecoveryState{}, err
	}
	var recovery RecoveryState
	if err := json.Unmarshal(data, &recovery); err != nil {
		return RecoveryState{}, err
	}
	return recovery, nil
}
