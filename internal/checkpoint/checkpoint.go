package checkpoint

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// Entry 单文件检查点
type Entry struct {
	Offset int64 `json:"offset"`
}

// Store 检查点存储，持久化到 JSON 文件
type Store struct {
	path string
	mu   sync.Mutex
	data map[string]Entry
}

// NewStore 创建检查点存储
func NewStore(path string) (*Store, error) {
	if path == "" {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, ".log-agent", "checkpoint.json")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	s := &Store{path: path, data: make(map[string]Entry)}
	if err := s.load(); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return s, nil
}

func (s *Store) load() error {
	b, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, &s.data)
}

func (s *Store) save() error {
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, b, 0644)
}

// Get 获取指定文件的检查点
func (s *Store) Get(filePath string) (offset int64, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.data[filePath]
	return e.Offset, ok
}

// Save 保存检查点（取 max，用于批量 ACK 后更新）
func (s *Store) Save(filePath string, offset int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.data[filePath]; ok && e.Offset > offset {
		return nil
	}
	s.data[filePath] = Entry{Offset: offset}
	return s.save()
}

// SaveMax 对同一文件多次调用，仅保留最大 offset
func (s *Store) SaveMax(filePath string, offset int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.data[filePath]
	if offset <= e.Offset {
		return nil
	}
	s.data[filePath] = Entry{Offset: offset}
	return s.save()
}
